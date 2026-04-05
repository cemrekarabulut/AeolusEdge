// Package http — HTTP middleware'leri.
//
// YENİ (v2): Rate Limiter + Request ID middleware eklendi.
//
// NEDEN RATE LIMITER:
//   IIoT gateway'ler brute-force ve flood saldırılarına maruz kalabilir.
//   Token bucket algoritması: burst'e izin verir ama sürekli yüksek hızı engeller.
//   Bu sayede meşru türbin verisi hiçbir zaman engellenmez.
//
// NEDEN REQUEST ID:
//   Dağıtık sistemlerde bir isteği log → Redis → analytics zincirinde takip etmek
//   için correlation ID şarttır. Her istek benzersiz ID alır, tüm loglara eklenir.
package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// requestIDKey — context'e request ID yazma anahtarı
type requestIDKey struct{}

// RequestID — context'ten request ID'yi okur
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// RequestIDMiddleware — her isteğe benzersiz X-Request-ID ekler
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			rand.Read(b)
			id = hex.EncodeToString(b)
		}
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tokenBucket — basit token bucket rate limiter (per-IP)
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	maxBurst float64
	rate     float64 // token/saniye
	lastTime time.Time
}

func (b *tokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastTime).Seconds()
	b.lastTime = now

	b.tokens += elapsed * b.rate
	if b.tokens > b.maxBurst {
		b.tokens = b.maxBurst
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimiter — per-IP token bucket rate limiter middleware
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64 // istek/saniye
	burst   float64 // maksimum anlık burst
	logger  *slog.Logger
}

// NewRateLimiter — rate: istek/saniye, burst: anlık maksimum
func NewRateLimiter(rate, burst float64, logger *slog.Logger) *RateLimiter {
	rl := &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rate,
		burst:   burst,
		logger:  logger,
	}
	// Eski bucket'ları temizle (bellek sızıntısı önleme)
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) getBucket(ip string) *tokenBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if b, ok := rl.buckets[ip]; ok {
		return b
	}
	b := &tokenBucket{
		tokens:   rl.burst,
		maxBurst: rl.burst,
		rate:     rl.rate,
		lastTime: time.Now(),
	}
	rl.buckets[ip] = b
	return b
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		// 10 dakikadır aktif olmayan bucket'ları sil
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, b := range rl.buckets {
			b.mu.Lock()
			if b.lastTime.Before(cutoff) {
				delete(rl.buckets, ip)
			}
			b.mu.Unlock()
		}
		rl.mu.Unlock()
	}
}

// Limit — rate limiting middleware; limitin aşılması 429 döndürür
func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = xff
		}

		if !rl.getBucket(ip).Allow() {
			rl.logger.Warn("rate limit aşıldı",
				slog.String("ip", ip),
				slog.String("path", r.URL.Path),
			)
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

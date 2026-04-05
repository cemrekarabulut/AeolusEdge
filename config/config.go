// Package config — uygulama yapılandırması.
//
// NEDEN ENV VAR (config dosyası yerine):
//   - 12-factor app prensibi: konfigürasyon ortamdan gelir
//   - Docker/K8s secret'ları env var olarak inject edilir
//   - Kaynak koda config değeri gömülmez → güvenli
//   - Farklı ortamlar (dev/staging/prod) için aynı binary, farklı env
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config — tüm uygulama ayarları tek yapıda.
// Flat yapı: nested config okunması zor, debug'ı güç.
type Config struct {
	// HTTP Server
	HTTPAddr string // dinleme adresi, örn: ":8080"

	// Logging
	LogLevel string // debug | info | warn | error

	// Auth
	JWTSecret string
	JWTTTL    time.Duration

	// Redis
	RedisAddr     string
	RedisDB       int
	RedisPassword string // boş olabilir

	// Worker Pool
	WorkerCount   int // goroutine sayısı
	WorkerBufSize int // channel buffer kapasitesi

	// Circuit Breaker
	CBThreshold int           // ardışık hata eşiği
	CBTimeout   time.Duration // Open'da kalınacak süre
}

// Load — env var'lardan config yükler.
// Zorunlu alanlar eksikse panic: erken başarısızlık, sessiz yanlış değer yok.
func Load() *Config {
	return &Config{
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		LogLevel:      env("LOG_LEVEL", "info"),
		JWTSecret:     mustEnv("JWT_SECRET"),
		JWTTTL:        time.Duration(envInt("JWT_TTL_HOURS", 24)) * time.Hour,
		RedisAddr:     env("REDIS_ADDR", "localhost:6379"),
		RedisDB:       envInt("REDIS_DB", 0),
		RedisPassword: env("REDIS_PASSWORD", ""),
		WorkerCount:   envInt("WORKER_COUNT", 16),
		WorkerBufSize: envInt("WORKER_BUF_SIZE", 512),
		CBThreshold:   envInt("CB_THRESHOLD", 5),
		CBTimeout:     time.Duration(envInt("CB_TIMEOUT_S", 30)) * time.Second,
	}
}

// String — hassas alanları maskeler, debug loglaması için güvenli.
func (c *Config) String() string {
	secret := "***"
	if len(c.JWTSecret) > 4 {
		secret = c.JWTSecret[:4] + "***"
	}
	return fmt.Sprintf(
		"Config{addr=%s log=%s redis=%s workers=%d buf=%d cb=%d/%s jwt=%s}",
		c.HTTPAddr, c.LogLevel, c.RedisAddr,
		c.WorkerCount, c.WorkerBufSize,
		c.CBThreshold, c.CBTimeout, secret,
	)
}

// --- yardımcı fonksiyonlar ---

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("zorunlu env var eksik: %q — lütfen ayarlayın", key))
	}
	return v
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		fmt.Fprintf(os.Stderr, "UYARI: %s geçersiz int değeri, default=%d kullanılıyor\n", key, fallback)
	}
	return fallback
}

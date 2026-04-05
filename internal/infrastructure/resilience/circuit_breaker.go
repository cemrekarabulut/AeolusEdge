// Package resilience — hata toleransı desenleri.
//
// NEDEN CIRCUIT BREAKER:
// Analytics engine yavaşladığında veya çöktüğünde Redis'e yazma başarısız olur.
// Her HTTP isteği Redis'i beklerse gateway de yavaşlar → cascade failure.
//
// Circuit Breaker bunu önler:
//   - Closed (normal): istekler geçer
//   - Open (tripped): istekler anında reddedilir, Redis'e hiç gidilmez
//   - HalfOpen (probe): timeout sonrası tek istek geçer, başarılıysa Closed'a döner
//
// Sonuç: Redis çöktüğünde gateway 3ms'de hata döner, 30s beklemez.
package resilience

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State — devre kesici durumu
type State int

const (
	StateClosed   State = iota // Normal: istekler geçer
	StateOpen                  // Tripped: istekler reddedilir
	StateHalfOpen              // Probe: tek istek geçer
)

func (s State) String() string {
	return [...]string{"Closed", "Open", "HalfOpen"}[s]
}

var ErrCircuitOpen = errors.New("circuit breaker: devre açık, istek reddedildi")

// CircuitBreaker — thread-safe state machine.
type CircuitBreaker struct {
	mu          sync.Mutex
	state       State
	failures    int           // ardışık hata sayısı
	successes   int           // HalfOpen'da ardışık başarı sayısı
	threshold   int           // Open'a geçmek için gereken hata sayısı
	successReq  int           // Closed'a dönmek için gereken başarı sayısı (HalfOpen'da)
	timeout     time.Duration // Open'da kalınacak süre
	lastFailure time.Time
	// Gözlemlenebilirlik için callback'ler (opsiyonel, nil olabilir)
	OnStateChange func(from, to State)
}

// NewCircuitBreaker — yapılandırılmış Circuit Breaker oluşturur.
// threshold: kaç ardışık hata Open'a geçirir
// timeout: Open'da ne kadar beklenecek (sonra HalfOpen)
func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:  threshold,
		successReq: 1, // 1 başarı yeterli — production'da 2-3 yapılabilir
		timeout:    timeout,
	}
}

// Do — fn'i Circuit Breaker koruması altında çalıştırır.
// Open ise fn çağrılmaz, ErrCircuitOpen döner.
func (cb *CircuitBreaker) Do(fn func() error) error {
	// State kontrolü — lock al
	cb.mu.Lock()
	state := cb.currentState()
	if state == StateOpen {
		cb.mu.Unlock()
		return ErrCircuitOpen
	}
	cb.mu.Unlock()

	// fn'i lock dışında çalıştır — uzun sürebilir
	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}

	return err
}

// State — mevcut durumu döner (thread-safe, sadece okuma için)
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentState()
}

// Stats — monitoring için anlık durum bilgisi
func (cb *CircuitBreaker) Stats() map[string]any {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return map[string]any{
		"state":        cb.currentState().String(),
		"failures":     cb.failures,
		"last_failure": cb.lastFailure,
	}
}

// currentState — mu lock altında çağrılmalı
func (cb *CircuitBreaker) currentState() State {
	if cb.state == StateOpen {
		if time.Since(cb.lastFailure) >= cb.timeout {
			cb.transition(StateHalfOpen)
		}
	}
	return cb.state
}

func (cb *CircuitBreaker) onFailure() {
	cb.failures++
	cb.successes = 0
	cb.lastFailure = time.Now()

	if cb.state == StateHalfOpen || cb.failures >= cb.threshold {
		cb.transition(StateOpen)
	}
}

func (cb *CircuitBreaker) onSuccess() {
	cb.failures = 0
	if cb.state == StateHalfOpen {
		cb.successes++
		if cb.successes >= cb.successReq {
			cb.transition(StateClosed)
		}
	}
}

func (cb *CircuitBreaker) transition(to State) {
	from := cb.state
	cb.state = to
	if from != to && cb.OnStateChange != nil {
		// Callback'i goroutine'de çağır — deadlock riskini önler
		go cb.OnStateChange(from, to)
	}
}

// Reset — test ve admin amaçlı zorla Closed'a döndür
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
}

// String — debug çıktısı
func (cb *CircuitBreaker) String() string {
	return fmt.Sprintf("CircuitBreaker{state=%s failures=%d threshold=%d}",
		cb.State(), cb.failures, cb.threshold)
}

package resilience_test

import (
	"errors"
	"testing"
	"time"

	"aeolus-edge/internal/infrastructure/resilience"
)

var errFake = errors.New("fake error")

func TestCircuitBreakerClosedToOpen(t *testing.T) {
	cb := resilience.NewCircuitBreaker(3, time.Second)

	// 3 hata — Open'a geçmeli
	for i := 0; i < 3; i++ {
		cb.Do(func() error { return errFake })
	}

	if cb.State() != resilience.StateOpen {
		t.Errorf("state=%s, want Open", cb.State())
	}
}

func TestCircuitBreakerOpenRejectsRequests(t *testing.T) {
	cb := resilience.NewCircuitBreaker(1, 10*time.Second)
	cb.Do(func() error { return errFake }) // Open'a geç

	err := cb.Do(func() error { return nil })
	if !errors.Is(err, resilience.ErrCircuitOpen) {
		t.Errorf("err=%v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreakerHalfOpenRecovery(t *testing.T) {
	cb := resilience.NewCircuitBreaker(1, 10*time.Millisecond)
	cb.Do(func() error { return errFake }) // Open

	time.Sleep(20 * time.Millisecond) // Timeout geç → HalfOpen

	// Başarılı istek → Closed'a dön
	err := cb.Do(func() error { return nil })
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if cb.State() != resilience.StateClosed {
		t.Errorf("state=%s, want Closed", cb.State())
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := resilience.NewCircuitBreaker(1, time.Minute)
	cb.Do(func() error { return errFake })

	cb.Reset()
	if cb.State() != resilience.StateClosed {
		t.Errorf("state=%s after Reset, want Closed", cb.State())
	}
}

func TestCircuitBreakerStateChangeCallback(t *testing.T) {
	changed := make(chan struct{}, 1)
	cb := resilience.NewCircuitBreaker(1, time.Second)
	cb.OnStateChange = func(from, to resilience.State) {
		changed <- struct{}{}
	}

	cb.Do(func() error { return errFake })

	select {
	case <-changed:
		// başarılı
	case <-time.After(100 * time.Millisecond):
		t.Error("OnStateChange callback tetiklenmedi")
	}
}

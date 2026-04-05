package workerpool_test

import (
	"sync/atomic"
	"testing"
	"time"

	"aeolus-edge/pkg/workerpool"
)

// TestWorkerPoolProcessesAllJobs — tüm job'ların işlendiğini doğrular
func TestWorkerPoolProcessesAllJobs(t *testing.T) {
	var processed atomic.Int64
	const total = 100

	pool := workerpool.New[int](4, 128, func(n int) {
		processed.Add(1)
	})

	for i := 0; i < total; i++ {
		pool.Submit(i)
	}

	pool.Shutdown()

	if got := processed.Load(); got != total {
		t.Errorf("processed=%d want=%d", got, total)
	}
}

// TestWorkerPoolBackpressure — dolu buffer'da drop sayacının artmasını doğrular
func TestWorkerPoolBackpressure(t *testing.T) {
	// 1 worker, 0 buffer — her Submit anında dolu olacak
	pool := workerpool.New[int](1, 0, func(n int) {
		time.Sleep(50 * time.Millisecond) // yavaş işlem simülasyonu
	})

	dropped := 0
	for i := 0; i < 20; i++ {
		if !pool.Submit(i) {
			dropped++
		}
	}

	pool.Shutdown()

	if dropped == 0 {
		t.Error("beklenen drop gerçekleşmedi")
	}
	if pool.DroppedCount() != int64(dropped) {
		t.Errorf("DroppedCount=%d dropped=%d uyuşmuyor", pool.DroppedCount(), dropped)
	}
}

// TestWorkerPoolShutdownSafety — Shutdown sonrası Submit false döner
func TestWorkerPoolShutdownSafety(t *testing.T) {
	pool := workerpool.New[int](2, 10, func(n int) {})
	pool.Shutdown()

	if pool.Submit(42) {
		t.Error("kapalı pool'a Submit true döndü")
	}
}

// TestWorkerPoolConcurrency — race detector ile paralel Submit testi
// go test -race ile çalıştır
func TestWorkerPoolConcurrency(t *testing.T) {
	var processed atomic.Int64
	pool := workerpool.New[int](8, 256, func(n int) {
		processed.Add(1)
	})

	// 10 goroutine aynı anda Submit yapar
	done := make(chan struct{})
	for g := 0; g < 10; g++ {
		go func() {
			for i := 0; i < 50; i++ {
				pool.Submit(i)
			}
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	pool.Shutdown()
	// En az bazıları işlendi (drop olabilir ama panic olmaz)
	t.Logf("processed=%d dropped=%d", processed.Load(), pool.DroppedCount())
}

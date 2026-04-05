// Package workerpool — sabit boyutlu goroutine havuzu.
//
// NEDEN WORKER POOL:
// Go goroutine'leri ucuzdur (~2KB stack) ama sınırsız spawn 3 soruna yol açar:
//  1. Ani yük patlamalarında toplam RAM tahmin edilemez hale gelir
//  2. Downstream Redis bağlantı havuzu tükenir (connection pool exhaustion)
//  3. OS scheduler context-switch overhead artar
//
// Worker Pool bu sorunları çözer:
//  - Sabit N goroutine → öngörülebilir CPU/RAM kullanımı
//  - Buffered channel → backpressure mekanizması
//  - atomic.Int64 → lock-free drop counter (hot path'te mutex yok)
package workerpool

import (
	"sync"
	"sync/atomic"
)

// Pool — generic, sabit boyutlu goroutine havuzu.
// T: işlenecek iş birimi tipi (örn: entity.SensorReading)
type Pool[T any] struct {
	jobs    chan T        // buffered iş kanalı
	wg      sync.WaitGroup
	dropped atomic.Int64 // lock-free: mutex olmadan okunabilir/yazılabilir
	closed  atomic.Bool  // Shutdown sonrası Submit'i güvenli reddet
}

// New — `workers` sayıda goroutine başlatır, `bufSize` kapasiteli buffer oluşturur.
// handler: her job için çağrılacak saf fonksiyon (panic-safe değil, recover eklenebilir)
func New[T any](workers, bufSize int, handler func(T)) *Pool[T] {
	p := &Pool[T]{
		jobs: make(chan T, bufSize),
	}

	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer p.wg.Done()
			for job := range p.jobs {
				// Panic recovery: tek worker patlasa diğerleri çalışmaya devam eder
				func() {
					defer func() {
						if r := recover(); r != nil {
							// Production'da slog ile logla
							_ = r
						}
					}()
					handler(job)
				}()
			}
		}()
	}

	return p
}

// Submit — job'ı non-blocking olarak kuyruğa ekler.
// Buffer dolu ise false döner ve dropped sayacını artırır.
// NEDEN NON-BLOCKING: HTTP handler'ı bloklamamak için kritik.
// Bir HTTP isteği bekleyemez; ya kabul et ya reddet.
func (p *Pool[T]) Submit(job T) bool {
	if p.closed.Load() {
		return false
	}
	select {
	case p.jobs <- job:
		return true
	default:
		p.dropped.Add(1) // atomic: lock yok, nanosaniye hızında
		return false
	}
}

// Shutdown — açık kanalı kapatır, tüm worker'ların bitmesini bekler.
// Çağrı sonrası Submit false döner. Graceful shutdown için tasarlandı.
func (p *Pool[T]) Shutdown() {
	p.closed.Store(true)
	close(p.jobs) // worker'lara "iş bitti" sinyali
	p.wg.Wait()   // tüm in-flight job'lar tamamlanana kadar bekle
}

// DroppedCount — Prometheus metriği veya alert için kullanılır.
// atomic.Load: herhangi bir goroutine'den thread-safe çağrılabilir.
func (p *Pool[T]) DroppedCount() int64 {
	return p.dropped.Load()
}

// QueueSize — anlık buffer doluluk oranı. Monitoring için.
func (p *Pool[T]) QueueSize() int {
	return len(p.jobs)
}

// Capacity — toplam buffer kapasitesi.
func (p *Pool[T]) Capacity() int {
	return cap(p.jobs)
}

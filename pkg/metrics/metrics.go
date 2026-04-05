// Package metrics — Prometheus metrik tanımları.
//
// İYİLEŞTİRME (v2): WebSocketClients gauge hub'dan gerçek zamanlı güncellenir.
// Tüm sayaçlar usecase/handler tarafından artık gerçekten çağrılıyor.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ReadingsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name: "readings_total",
		Help: "Başarıyla kuyruğa alınan toplam sensör okuma sayısı",
	}, []string{"device_id"})

	ReadingsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name: "readings_dropped_total",
		Help: "Worker pool buffer dolu olduğunda atlanan okuma sayısı",
	})

	ReadingsInvalid = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name: "readings_invalid_total",
		Help: "Domain validasyonunu geçemeyen okuma sayısı",
	}, []string{"device_id"})

	WorkerPoolQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name: "worker_pool_queue_depth_ratio",
		Help: "Worker pool buffer doluluk oranı (0=boş 1=tam dolu)",
	})

	CircuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name: "circuit_breaker_state",
		Help: "Circuit breaker durumu: 0=Closed 1=HalfOpen 2=Open",
	}, []string{"service"})

	WebSocketClients = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name: "websocket_clients_active",
		Help: "Aktif WebSocket bağlantı sayısı",
	})

	AnomaliesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "aeolus", Subsystem: "analytics",
		Name: "anomalies_total",
		Help: "Tespit edilen toplam anomali sayısı",
	}, []string{"device_id", "metric", "severity"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name:    "http_request_duration_seconds",
		Help:    "HTTP istek işlem süresi",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	RedisStreamLen = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "aeolus", Subsystem: "redis",
		Name: "stream_pending_messages",
		Help: "Redis stream'deki işlenmemiş mesaj sayısı (consumer lag)",
	})

	// YENİ (v2): rate limit istatistikleri
	RateLimitHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "aeolus", Subsystem: "gateway",
		Name: "rate_limit_hits_total",
		Help: "Rate limit aşım sayısı",
	}, []string{"ip"})
)

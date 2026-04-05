// Package port — Hexagonal Architecture'ın "port" tarafı.
// Bu interface'ler domain'in dışarıya ne beklediğini tanımlar.
// Neden interface: usecase sadece bu kontratı bilir, Redis/HTTP gibi detayları bilmez.
// Bu sayede Redis yerine Kafka geçsen sadece infrastructure değişir, usecase dokunulmaz.
package port

import (
	"context"

	"aeolus-edge/internal/domain/entity"
	"aeolus-edge/internal/domain/event"
)

// ReadingQueue — sensör verisini asenkron kuyruğa alan adaptör kontratı.
// Gerçek implementasyon: internal/infrastructure/redis/producer.go
type ReadingQueue interface {
	Enqueue(ctx context.Context, r entity.SensorReading) error
}

// AlertBroadcaster — anomali eventini bağlı istemcilere ileten kontrat.
// Gerçek implementasyon: internal/infrastructure/websocket/hub.go
type AlertBroadcaster interface {
	Broadcast(evt event.AnomalyEvent) error
}

// HealthChecker — bileşen sağlık durumunu sorgular.
// Circuit Breaker ve /health endpoint tarafından kullanılır.
type HealthChecker interface {
	Ping(ctx context.Context) error
}

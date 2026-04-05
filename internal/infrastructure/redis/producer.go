// Package redis — Redis adaptörleri (Hexagonal Architecture: driven side).
//
// NEDEN REDIS STREAM (XAdd) yerine basit List (LPush):
//   - Stream: consumer group'lar → birden fazla analytics instance paralel tüketebilir
//   - Stream: XACK ile "at-least-once delivery" garantisi (işlenmeden çöken mesaj kaybolmaz)
//   - Stream: MAXLEN ~ ile bellek otomatik sınırlandırılır (O(1) trimming)
//   - List: RPOPLPUSH ile benzer yapı kurulabilir ama consumer group desteği yok
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"aeolus-edge/internal/domain/entity"
)

const (
	StreamKey      = "sensor:readings"
	MaxStreamLen   = 10_000 // ~10k mesaj — yaklaşık 10MB
)

// StreamProducer — entity.SensorReading'i Redis Stream'e yazar.
// port.ReadingQueue interface'ini implemente eder.
type StreamProducer struct {
	client *redis.Client
	logger *slog.Logger
}

// NewStreamProducer — dependency injection ile oluşturulur.
// client dışarıdan verilir → test'te mock/miniredis kullanılabilir.
func NewStreamProducer(client *redis.Client, logger *slog.Logger) *StreamProducer {
	return &StreamProducer{client: client, logger: logger}
}

// Enqueue — port.ReadingQueue interface'ini implement eder.
// NEDEN MAXLEN Approx=true: Exact trimming O(N), Approx O(1). Performans farkı önemli.
func (p *StreamProducer) Enqueue(ctx context.Context, r entity.SensorReading) error {
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal hatası: %w", err)
	}

	args := &redis.XAddArgs{
		Stream: StreamKey,
		MaxLen: MaxStreamLen,
		Approx: true, // MAXLEN ~ : Redis iç optimizasyonuna bırak
		ID:     "*",  // Redis otomatik ID atar (timestamp-sequence)
		Values: map[string]any{
			"device_id": r.DeviceID,
			"payload":   string(payload),
			"ts":        r.Timestamp.UnixMilli(),
		},
	}

	id, err := p.client.XAdd(ctx, args).Result()
	if err != nil {
		return fmt.Errorf("xadd hatası: %w", err)
	}

	p.logger.DebugContext(ctx, "reading kuyruğa alındı",
		slog.String("stream_id", id),
		slog.String("device", r.DeviceID),
		slog.Float64("vibration", r.Vibration),
		slog.Float64("rpm", r.RPM),
		slog.Float64("temperature", r.Temperature),
	)

	return nil
}

// Ping — HealthChecker interface'i için. Circuit Breaker health probe'u.
func (p *StreamProducer) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return p.client.Ping(ctx).Err()
}

// StreamLen — monitoring için: stream'deki mesaj sayısı.
func (p *StreamProducer) StreamLen(ctx context.Context) (int64, error) {
	return p.client.XLen(ctx, StreamKey).Result()
}

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"aeolus-edge/internal/domain/event"
)

const AlertChannel = "anomaly:alerts"

// AlertBroadcaster — mesaj iletme kontratı (websocket hub bunu implemente eder)
type AlertBroadcaster interface {
	BroadcastJSON(data []byte)
}

// AlertSubscriber — Redis Pub/Sub'ı dinler, gelen alert'leri WebSocket Hub'a iletir.
//
// NEDEN PUB/SUB (Stream yerine):
//   - Alert'ler anlık bildirimdir, birden fazla kez tüketilmesi gerekmez
//   - WebSocket'e iletilmesi yeterli; persistent storage gerekmez
//   - Pub/Sub bu senaryo için Stream'den daha düşük gecikme sağlar
//   - Fan-out: birden fazla gateway instance aynı alert'i alabilir
type AlertSubscriber struct {
	client      *redis.Client
	broadcaster AlertBroadcaster
	logger      *slog.Logger
}

func NewAlertSubscriber(client *redis.Client, broadcaster AlertBroadcaster, logger *slog.Logger) *AlertSubscriber {
	return &AlertSubscriber{
		client:      client,
		broadcaster: broadcaster,
		logger:      logger,
	}
}

// Run — context iptal edilene kadar çalışır.
// Bağlantı kesilirse otomatik yeniden abone olur (reconnect loop).
func (s *AlertSubscriber) Run(ctx context.Context) {
	s.logger.Info("alert subscriber başladı", slog.String("channel", AlertChannel))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("alert subscriber durduruldu")
			return
		default:
		}

		if err := s.subscribe(ctx); err != nil {
			if ctx.Err() != nil {
				return // context iptal edildi, normal çıkış
			}
			s.logger.Error("pubsub bağlantısı koptu, 3s sonra yeniden bağlanıyor",
				slog.String("err", err.Error()))
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (s *AlertSubscriber) subscribe(ctx context.Context) error {
	pubsub := s.client.Subscribe(ctx, AlertChannel)
	defer pubsub.Close()

	// Abonelik onayını bekle
	if _, err := pubsub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe hatası: %w", err)
	}

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("pubsub kanalı kapandı")
			}
			s.handleMessage(msg.Payload)
		}
	}
}

func (s *AlertSubscriber) handleMessage(payload string) {
	// Önce parse et — geçersiz JSON'ı WebSocket'e gönderme
	var events []event.AnomalyEvent
	if err := json.Unmarshal([]byte(payload), &events); err != nil {
		s.logger.Warn("geçersiz alert payload",
			slog.String("err", err.Error()),
			slog.String("payload", payload),
		)
		return
	}

	s.logger.Info("anomali alert'i iletiliyor",
		slog.Int("event_count", len(events)),
		slog.String("device", events[0].DeviceID),
	)

	// Doğrudan ham JSON'ı ilet — zero-copy re-marshal gereksiz
	s.broadcaster.BroadcastJSON([]byte(payload))
}

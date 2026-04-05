// Package usecase — iş mantığı katmanı.
//
// İYİLEŞTİRMELER (v2):
//   1. Prometheus sayaçları artık gerçekten güncelleniyor (önceki sürümde eksikti)
//   2. InvalidCount ayrı tutuldu — dropped ile karışmıyordu
//   3. Stats() queue_util_pct hesabı düzeltildi (Capacity=0 guard)
package usecase

import (
	"context"
	"log/slog"

	"aeolus-edge/internal/domain/entity"
	"aeolus-edge/internal/domain/port"
	"aeolus-edge/pkg/metrics"
	"aeolus-edge/pkg/workerpool"
)

type IngestUseCase struct {
	pool   *workerpool.Pool[entity.SensorReading]
	logger *slog.Logger
}

func New(queue port.ReadingQueue, workers, bufSize int, logger *slog.Logger) *IngestUseCase {
	handler := func(r entity.SensorReading) {
		ctx := context.Background()
		if err := queue.Enqueue(ctx, r); err != nil {
			logger.Error("kuyruğa alma hatası",
				slog.String("device", r.DeviceID),
				slog.String("err", err.Error()),
			)
		} else {
			// DÜZELTME: başarılı enqueue Prometheus'a yazılıyor
			metrics.ReadingsTotal.WithLabelValues(r.DeviceID).Inc()
		}
	}
	return &IngestUseCase{
		pool:   workerpool.New(workers, bufSize, handler),
		logger: logger,
	}
}

func (uc *IngestUseCase) Handle(r entity.SensorReading) {
	if !r.IsValid() {
		uc.logger.Warn("geçersiz sensör verisi reddedildi",
			slog.String("device", r.DeviceID),
			slog.Float64("vibration", r.Vibration),
			slog.Float64("rpm", r.RPM),
		)
		// DÜZELTME: geçersiz veri sayacı güncelleniyor
		metrics.ReadingsInvalid.WithLabelValues(r.DeviceID).Inc()
		return
	}

	if ok := uc.pool.Submit(r); !ok {
		uc.logger.Warn("worker pool dolu — veri atlandı",
			slog.String("device", r.DeviceID),
			slog.Int64("toplam_atlanan", uc.pool.DroppedCount()),
		)
		// DÜZELTME: drop sayacı Prometheus'a yazılıyor
		metrics.ReadingsDropped.Inc()
	}
}

func (uc *IngestUseCase) Stats() map[string]any {
	cap := uc.pool.Capacity()
	util := 0.0
	if cap > 0 {
		util = float64(uc.pool.QueueSize()) / float64(cap) * 100
	}
	// DÜZELTME: gauge gerçek zamanlı güncelleniyor
	metrics.WorkerPoolQueueDepth.Set(util / 100)

	return map[string]any{
		"dropped":        uc.pool.DroppedCount(),
		"queue_size":     uc.pool.QueueSize(),
		"queue_cap":      cap,
		"queue_util_pct": util,
	}
}

func (uc *IngestUseCase) Shutdown() {
	uc.logger.Info("ingest usecase kapatılıyor — in-flight job'lar bekleniyor")
	uc.pool.Shutdown()
	uc.logger.Info("ingest usecase kapatıldı",
		slog.Int64("toplam_atlanan", uc.pool.DroppedCount()))
}

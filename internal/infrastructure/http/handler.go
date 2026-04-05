// Package http — HTTP adaptörü (Hexagonal: driving side).
//
// SORUMLULUK SINIRI:
// Bu dosya sadece HTTP protokolünü bilir.
// İş mantığı yok — sadece: parse → validate → usecase'e ver → cevap dön.
// Neden: HTTP değişirse (gRPC, MQTT) sadece bu dosya değişir, usecase dokunulmaz.
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"aeolus-edge/internal/domain/entity"
	"aeolus-edge/internal/infrastructure/auth"
	"aeolus-edge/internal/usecase"
)

// ingestRequest — HTTP transport DTO.
// Domain entity'sinden kasıtlı ayrı tutuldu:
// API değişiklikleri (yeni alan, rename) domain'i kirletmez.
type ingestRequest struct {
	DeviceID    string  `json:"device_id"`
	Vibration   float64 `json:"vibration"`
	RPM         float64 `json:"rpm"`
	Temperature float64 `json:"temperature"`
	Timestamp   float64 `json:"timestamp"` // Unix epoch (saniye, float)
}

// healthResponse — /health endpoint yanıtı
type healthResponse struct {
	Status  string         `json:"status"`
	Service string         `json:"service"`
	Stats   map[string]any `json:"stats,omitempty"`
}

// IngestHandler — /ingest POST endpoint'i
type IngestHandler struct {
	uc     *usecase.IngestUseCase
	logger *slog.Logger
}

func NewIngestHandler(uc *usecase.IngestUseCase, logger *slog.Logger) *IngestHandler {
	return &IngestHandler{uc: uc, logger: logger}
}

func (h *IngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// device_id JWT middleware tarafından inject edilmiş olmalı
	deviceID, ok := r.Context().Value(auth.DeviceIDKey).(string)
	if !ok || deviceID == "" {
		http.Error(w, "device kimliği eksik", http.StatusBadRequest)
		return
	}

	// 1MB limit — büyük payload saldırılarına karşı
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()

	var req ingestRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // strict — beklenmeyen alan varsa reddet
	if err := dec.Decode(&req); err != nil {
		h.logger.WarnContext(r.Context(), "geçersiz istek gövdesi",
			slog.String("err", err.Error()),
			slog.String("device", deviceID),
		)
		http.Error(w, "geçersiz JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// DTO → Domain Entity
	// JWT'deki device_id kullan — body'dekini değil (spoof koruması)
	ts := time.Now()
	if req.Timestamp > 0 {
		ts = time.Unix(int64(req.Timestamp), 0)
	}

	reading := entity.SensorReading{
		DeviceID:    deviceID,
		Timestamp:   ts,
		Vibration:   req.Vibration,
		RPM:         req.RPM,
		Temperature: req.Temperature,
	}

	// Non-blocking — worker pool'a teslim et ve hemen dön
	h.uc.Handle(reading)

	// 202 Accepted: "aldım, asenkron işliyorum"
	// 200 OK değil: 200 "tamamen işlendi" anlamına gelir
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "accepted",
		"device_id": deviceID,
	})
}

// HealthHandler — /health endpoint: liveness + stats
type HealthHandler struct {
	uc     *usecase.IngestUseCase
	logger *slog.Logger
}

func NewHealthHandler(uc *usecase.IngestUseCase, logger *slog.Logger) *HealthHandler {
	return &HealthHandler{uc: uc, logger: logger}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:  "ok",
		Service: "aeolus-edge",
		Stats:   h.uc.Stats(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

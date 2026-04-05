package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"aeolus-edge/internal/domain/entity"
	"aeolus-edge/internal/infrastructure/auth"
	infrahttp "aeolus-edge/internal/infrastructure/http"
	"aeolus-edge/internal/usecase"
)

// mockQueue — port.ReadingQueue'nun test implementasyonu
type mockQueue struct {
	received []entity.SensorReading
	err      error
}

func (m *mockQueue) Enqueue(_ context.Context, r entity.SensorReading) error {
	if m.err != nil {
		return m.err
	}
	m.received = append(m.received, r)
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func makeRequest(t *testing.T, deviceID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	logger := testLogger()
	q := &mockQueue{}
	uc := usecase.New(q, 2, 10, logger)
	defer uc.Shutdown()

	handler := infrahttp.NewIngestHandler(uc, logger)

	// JWT token üret
	const secret = "test-secret-minimum-32-chars-long"
	token, err := auth.GenerateToken(deviceID, secret, 3600*1000*1000*1000) // 1 saat
	if err != nil {
		t.Fatalf("token üretilemedi: %v", err)
	}

	mw := auth.NewMiddleware(secret, logger)

	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/ingest", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	mw.Authenticate(handler).ServeHTTP(rec, req)
	return rec
}

func TestIngestHandler_ValidPayload(t *testing.T) {
	rec := makeRequest(t, "turbine-001", map[string]any{
		"vibration":   4.2,
		"rpm":         1800.0,
		"temperature": 72.5,
		"timestamp":   1700000000.0,
	})

	if rec.Code != http.StatusAccepted {
		t.Errorf("status=%d want 202, body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != "accepted" {
		t.Errorf("response status=%q want accepted", resp["status"])
	}
}

func TestIngestHandler_InvalidJSON(t *testing.T) {
	logger := testLogger()
	q := &mockQueue{}
	uc := usecase.New(q, 2, 10, logger)
	defer uc.Shutdown()

	const secret = "test-secret-minimum-32-chars-long"
	token, _ := auth.GenerateToken("turbine-001", secret, 3600*1000*1000*1000)
	mw := auth.NewMiddleware(secret, logger)
	handler := infrahttp.NewIngestHandler(uc, logger)

	req := httptest.NewRequest("POST", "/ingest", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	mw.Authenticate(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", rec.Code)
	}
}

func TestIngestHandler_WrongMethod(t *testing.T) {
	logger := testLogger()
	q := &mockQueue{}
	uc := usecase.New(q, 2, 10, logger)
	defer uc.Shutdown()

	handler := infrahttp.NewIngestHandler(uc, logger)
	req := httptest.NewRequest("GET", "/ingest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d want 405", rec.Code)
	}
}

func TestIngestHandler_NoAuth(t *testing.T) {
	logger := testLogger()
	q := &mockQueue{}
	uc := usecase.New(q, 2, 10, logger)
	defer uc.Shutdown()

	const secret = "test-secret-minimum-32-chars-long"
	mw := auth.NewMiddleware(secret, logger)
	handler := infrahttp.NewIngestHandler(uc, logger)

	b, _ := json.Marshal(map[string]any{"vibration": 4.2, "rpm": 1800.0, "temperature": 72.5})
	req := httptest.NewRequest("POST", "/ingest", bytes.NewReader(b))
	// Authorization header YOK

	rec := httptest.NewRecorder()
	mw.Authenticate(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", rec.Code)
	}
}

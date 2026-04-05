package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"log/slog"
	"os"

	infrahttp "aeolus-edge/internal/infrastructure/http"
)

func TestRequestIDMiddleware_AddsHeader(t *testing.T) {
	handler := infrahttp.RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := infrahttp.RequestID(r.Context())
		if id == "" {
			t.Error("context'te request ID yok")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header eksik")
	}
}

func TestRequestIDMiddleware_PropagatesExisting(t *testing.T) {
	const existingID = "test-correlation-id-123"
	handler := infrahttp.RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := infrahttp.RequestID(r.Context())
		if id != existingID {
			t.Errorf("request ID=%q want %q", id, existingID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", existingID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	rl := infrahttp.NewRateLimiter(100, 10, logger)

	handler := rl.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/ingest", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("istek %d: status=%d want 200", i, rec.Code)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	// 2 burst, çok düşük rate
	rl := infrahttp.NewRateLimiter(0.001, 2, logger)

	handler := rl.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	blocked := 0
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/ingest", nil)
		req.RemoteAddr = "10.0.0.1:9999"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			blocked++
		}
	}
	if blocked == 0 {
		t.Error("rate limit hiç tetiklenmedi")
	}
}

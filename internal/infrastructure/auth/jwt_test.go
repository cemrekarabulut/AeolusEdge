package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aeolus-edge/internal/infrastructure/auth"
	"log/slog"
	"os"
)

const testSecret = "test-secret-32-chars-minimum-len"

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestGenerateAndValidateToken(t *testing.T) {
	token, err := auth.GenerateToken("turbine-001", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("token üretilemedi: %v", err)
	}
	if token == "" {
		t.Fatal("token boş")
	}
}

func TestAuthenticateMiddleware_Valid(t *testing.T) {
	mw := auth.NewMiddleware(testSecret, testLogger())
	token, _ := auth.GenerateToken("turbine-001", testSecret, time.Hour)

	called := false
	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		deviceID, ok := r.Context().Value(auth.DeviceIDKey).(string)
		if !ok || deviceID != "turbine-001" {
			t.Errorf("device_id=%q want turbine-001", deviceID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/ingest", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("downstream handler çağrılmadı")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d want 200", rec.Code)
	}
}

func TestAuthenticateMiddleware_Missing(t *testing.T) {
	mw := auth.NewMiddleware(testSecret, testLogger())
	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler çağrılmamalıydı")
	}))

	req := httptest.NewRequest("POST", "/ingest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", rec.Code)
	}
}

func TestAuthenticateMiddleware_Expired(t *testing.T) {
	mw := auth.NewMiddleware(testSecret, testLogger())
	token, _ := auth.GenerateToken("turbine-001", testSecret, -time.Second) // geçmiş zaman

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler çağrılmamalıydı")
	}))

	req := httptest.NewRequest("POST", "/ingest", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", rec.Code)
	}
}

func TestAuthenticateMiddleware_WrongSecret(t *testing.T) {
	mw := auth.NewMiddleware(testSecret, testLogger())
	token, _ := auth.GenerateToken("turbine-001", "wrong-secret", time.Hour)

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler çağrılmamalıydı")
	}))

	req := httptest.NewRequest("POST", "/ingest", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", rec.Code)
	}
}

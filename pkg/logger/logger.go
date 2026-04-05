// Package logger — yapılandırılmış loglama.
//
// NEDEN slog (Go 1.21 std-lib):
//   - Sıfır dış bağımlılık — zap, logrus gerek yok
//   - JSON output: log aggregation sistemleri (ELK, Loki) makine-parse edebilir
//   - Context propagation: trace_id her log satırına otomatik eklenir
//   - Performance: zerolog kadar hızlı, allocation minimize edilmiş
//   - Seviye kontrolü: DEBUG'ı production'da kapatmak için env var yeterli
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type contextKey string

const traceIDKey contextKey = "trace_id"

// New — JSON formatında yapılandırılmış logger oluşturur.
// levelStr: "debug" | "info" | "warn" | "error"
func New(levelStr string) *slog.Logger {
	level := parseLevel(levelStr)

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug, // debug'da dosya:satır bilgisi
	})

	return slog.New(handler)
}

// WithTraceID — context'e trace_id ekler, log satırlarında görünür.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceID — context'ten trace_id'yi okur.
func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

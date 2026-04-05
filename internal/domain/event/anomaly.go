// Package event — domain eventleri. Sistemdeki önemli olayları temsil eder.
// Neden ayrı paket: Entity'ler "ne var"ı, event'ler "ne oldu"yu temsil eder.
package event

import "time"

// Severity — anomalinin ciddiyeti. Z-score eşiğine göre belirlenir.
type Severity string

const (
	SeverityWarning  Severity = "WARNING"  // 3σ ≤ z < 4σ
	SeverityError    Severity = "ERROR"    // 4σ ≤ z < 5σ
	SeverityCritical Severity = "CRITICAL" // z ≥ 5σ
)

// AnomalyEvent — bir metrikte istatistiksel sapma tespit edildiğinde yayınlanır.
// Analytics engine bu tipi JSON olarak Redis Pub/Sub'a yazar,
// Gateway subscriber bunu parse edip WebSocket'e iletir.
type AnomalyEvent struct {
	DeviceID  string    `json:"device_id"`
	Metric    string    `json:"metric"`    // "vibration" | "rpm" | "temperature"
	Value     float64   `json:"value"`     // Gerçek ölçüm değeri
	ZScore    float64   `json:"z_score"`   // Kaç standart sapma uzakta
	Mean      float64   `json:"mean"`      // Penceredeki ortalama
	StdDev    float64   `json:"std_dev"`   // Penceredeki standart sapma
	Severity  Severity  `json:"severity"`
	Timestamp time.Time `json:"timestamp"`
}

// ClassifySeverity — z-score'a göre severity belirler.
func ClassifySeverity(z float64) Severity {
	switch {
	case z >= 5.0:
		return SeverityCritical
	case z >= 4.0:
		return SeverityError
	default:
		return SeverityWarning
	}
}

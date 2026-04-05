// Package entity — domain'in merkezindeki saf veri yapıları.
// Hiçbir dış import yok; bu dosya Go std-lib dışında hiçbir şeye bağımlı değil.
// Neden: Clean Architecture'ın temel kuralı — domain dışarıyı bilmez.
package entity

import "time"

// SensorReading — bir türbinden gelen tek bir ölçüm kaydı.
// Immutable olarak tasarlanmıştır; worker'lar arasında güvenle paylaşılabilir.
type SensorReading struct {
	DeviceID    string    `json:"device_id"`
	Timestamp   time.Time `json:"timestamp"`
	Vibration   float64   `json:"vibration"`   // mm/s — titreşim hızı
	RPM         float64   `json:"rpm"`         // devir/dakika
	Temperature float64   `json:"temperature"` // °C — yatak sıcaklığı
}

// IsValid — temel domain validasyonu. Business rule: değerler fiziksel sınırlar içinde olmalı.
// HTTP katmanına sızdırmak yerine domain kuralı olarak burada tanımlandı.
func (r SensorReading) IsValid() bool {
	return r.DeviceID != "" &&
		!r.Timestamp.IsZero() &&
		r.Vibration >= 0 && r.Vibration <= 100 &&
		r.RPM >= 0 && r.RPM <= 5000 &&
		r.Temperature >= -40 && r.Temperature <= 300
}

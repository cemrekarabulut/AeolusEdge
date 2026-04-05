// Package auth — JWT tabanlı cihaz kimlik doğrulaması.
//
// NEDEN JWT (JSON Web Token):
// Endüstriyel IoT'de her türbin bir "device" olarak kayıtlıdır.
// Session tabanlı auth gateway'i stateful yapar → horizontal scaling zorlaşır.
// JWT stateless'tır: her token kendi kimliğini taşır, merkezi session store gerekmez.
// Türbin token'ını gateway restart'tan sonra da geçerlidir.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// contextKey — context'e yazılan değerlerin tip güvenli anahtarı.
// string yerine özel tip kullanmak: başka paketlerin aynı key'i kullanmasını önler.
type contextKey string

const DeviceIDKey contextKey = "device_id"

var (
	ErrMissingToken = errors.New("authorization header eksik")
	ErrInvalidToken = errors.New("token geçersiz veya süresi dolmuş")
	ErrWrongMethod  = errors.New("beklenmeyen imzalama metodu")
)

// Claims — JWT payload'ı. device_id bizim eklediğimiz özel alan.
type Claims struct {
	DeviceID string `json:"device_id"`
	jwt.RegisteredClaims
}

// Middleware — JWT doğrulama middleware'i.
type Middleware struct {
	secret []byte
	logger *slog.Logger
}

// NewMiddleware — JWT secret ile middleware oluşturur.
// secret production'da env var'dan gelmeli, kaynak koda gömülmemeli.
func NewMiddleware(secret string, logger *slog.Logger) *Middleware {
	return &Middleware{
		secret: []byte(secret),
		logger: logger,
	}
}

// Authenticate — HTTP handler'ı JWT koruması altına alır.
// Başarılı doğrulama sonrası device_id context'e inject edilir.
// Downstream handler: r.Context().Value(auth.DeviceIDKey).(string) ile okur.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := extractBearer(r)
		if err != nil {
			m.logger.WarnContext(r.Context(), "auth: token eksik",
				slog.String("remote", r.RemoteAddr),
				slog.String("path", r.URL.Path),
			)
			http.Error(w, ErrMissingToken.Error(), http.StatusUnauthorized)
			return
		}

		claims, err := m.parseToken(raw)
		if err != nil {
			m.logger.WarnContext(r.Context(), "auth: token geçersiz",
				slog.String("err", err.Error()),
				slog.String("remote", r.RemoteAddr),
			)
			http.Error(w, ErrInvalidToken.Error(), http.StatusUnauthorized)
			return
		}

		// device_id'yi context'e yaz — downstream handler'lar güvenle okuyabilir
		ctx := context.WithValue(r.Context(), DeviceIDKey, claims.DeviceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) parseToken(raw string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims,
		func(t *jwt.Token) (any, error) {
			// Algoritma doğrulaması kritik: "alg:none" saldırısını önler
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, ErrWrongMethod
			}
			return m.secret, nil
		},
		jwt.WithExpirationRequired(), // exp alanı zorunlu
		jwt.WithIssuedAt(),           // iat alanı zorunlu
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func extractBearer(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", ErrMissingToken
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", ErrMissingToken
	}
	return parts[1], nil
}

// GenerateToken — türbin/simülatör için JWT üretir.
// Production'da bu fonksiyon ayrı bir auth servisinde olmalı.
// PoC'de tokengen CLI tool'u bu fonksiyonu kullanır.
func GenerateToken(deviceID, secret string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "aeolus-edge",
			Subject:   deviceID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

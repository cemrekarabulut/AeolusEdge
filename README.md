# AeolusEdge

**IIoT Edge Gateway & Real-Time Anomali Tespit Sistemi**


---

## Mimariye Hızlı Bakış

```
Türbin Simülatörü (Python asyncio)
    │  HTTP POST /ingest  [JWT Bearer]
    ▼
Edge Gateway (Go 1.21)
    ├─ JWT Auth Middleware
    ├─ Worker Pool (16 goroutine, 512 buffer)
    ├─ Circuit Breaker (5 hata → Open)
    └─ Redis Stream Producer (MAXLEN~10000)
         │  XAdd  sensor:readings
         ▼
    Redis 7.2
         │  XREADGROUP  analytics-group
         ▼
Analytics Engine (Python asyncio)
    ├─ Sliding Window (60 okuma / metrik / cihaz)
    ├─ Z-Score Detector (eşik: 3σ)
    └─ Redis Pub/Sub Publisher  anomaly:alerts
         │
         ▼  Subscribe
    Gateway Alert Subscriber
         │  Broadcast
         ▼
    WebSocket Hub → Bağlı Dashboard'lar
```

---

## Kurulum

### Ön Gereksinimler

| Araç | Versiyon | Kurulum |
|---|---|---|
| Go | ≥ 1.21 | [go.dev/dl](https://go.dev/dl) |
| Python | ≥ 3.11 | [python.org](https://python.org) |
| Redis | ≥ 7.0 | `brew install redis` / `apt install redis` |
| Docker | herhangi | [docker.com](https://docker.com) |

### Seçenek A: Docker ile (Önerilen)

```bash
git clone <repo>
cd aeolus-edge

# Tüm servisleri başlat (ilk seferinde build eder)
make docker-up

# Servisleri izle
docker compose logs -f
```

### Seçenek B: Yerel Geliştirme

**Terminal 1 — Redis:**
```bash
make redis-start
```

**Terminal 2 — Gateway:**
```bash
export JWT_SECRET="supersecretkey-change-in-production-env"
make run
```

**Terminal 3 — Analytics Engine:**
```bash
cd simulator
pip install -r requirements.txt
python analytics_engine.py
```

**Terminal 4 — Simülatör:**
```bash
cd simulator
python turbine_simulator.py
```

---

## Endpoint'ler

| Endpoint | Metod | Açıklama |
|---|---|---|
| `/ingest` | POST | Sensör verisi al (JWT gerekli) |
| `/ws` | WS | Anomali alertleri WebSocket |
| `/health` | GET | Liveness probe |
| `/metrics` | GET | Prometheus metrikleri |
| `/stats` | GET | Anlık sistem istatistikleri |

---

## Test Etme

```bash
# Tüm testler
make test

# Sadece Go (race detector ile)
make test-go

# Sadece Python
make test-py

# Test token üret
make token
```

### Manuel Test

```bash
# Token üret
TOKEN=$(JWT_SECRET=supersecretkey-change-in-production-env \
  go run ./tools/tokengen --device turbine-001 --ttl 1h 2>/dev/null | grep "^Token" | awk '{print $3}')

# Sensör verisi gönder
curl -X POST http://localhost:8080/ingest \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"vibration":4.2,"rpm":1800,"temperature":72.5}'

# Health kontrol
curl http://localhost:8080/health | python3 -m json.tool

# İstatistikler
curl http://localhost:8080/stats | python3 -m json.tool

# WebSocket (wscat gerekli: npm i -g wscat)
wscat -c ws://localhost:8080/ws
```

---

## Konfigürasyon (Env Var)

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `JWT_SECRET` | **zorunlu** | JWT imzalama anahtarı |
| `HTTP_ADDR` | `:8080` | Gateway dinleme adresi |
| `LOG_LEVEL` | `info` | debug / info / warn / error |
| `REDIS_ADDR` | `localhost:6379` | Redis adresi |
| `WORKER_COUNT` | `16` | Worker pool goroutine sayısı |
| `WORKER_BUF_SIZE` | `512` | Worker pool buffer kapasitesi |
| `CB_THRESHOLD` | `5` | Circuit Breaker hata eşiği |
| `CB_TIMEOUT_S` | `30` | Circuit Breaker Open süresi (saniye) |
| `TURBINE_COUNT` | `5` | Simülasyon türbin sayısı |
| `WINDOW_SIZE` | `60` | Sliding window büyüklüğü |
| `Z_THRESHOLD` | `3.0` | Anomali Z-score eşiği |

---

## Proje Yapısı

```
aeolus-edge/
├── cmd/gateway/main.go              # Entrypoint — DI Composition Root
├── internal/
│   ├── domain/                      # Saf iş kuralları — dış bağımlılık SIFIR
│   │   ├── entity/reading.go        # SensorReading value object
│   │   ├── event/anomaly.go         # AnomalyEvent domain type
│   │   └── port/ports.go            # Interface kontratları
│   ├── usecase/ingest_reading.go    # Iş mantığı — Worker Pool yönetimi
│   └── infrastructure/              # Dış adaptörler
│       ├── auth/jwt.go              # JWT middleware
│       ├── http/handler.go          # HTTP adaptörü
│       ├── redis/producer.go        # Redis Stream yazıcı
│       ├── redis/subscriber.go      # Redis Pub/Sub dinleyici
│       ├── resilience/              # Circuit Breaker
│       └── websocket/hub.go        # Thread-safe WebSocket Hub
├── pkg/
│   ├── workerpool/pool.go          # Generic goroutine havuzu
│   ├── metrics/metrics.go           # Prometheus metrik tanımları
│   └── logger/logger.go            # Yapılandırılmış slog wrapper
├── config/config.go                 # Env var bazlı konfigürasyon
├── simulator/
│   ├── turbine_simulator.py        # Asyncio veri simülatörü
│   ├── analytics_engine.py         # Z-Score anomali tespiti
│   └── test_analytics.py           # Python birim testleri
├── tools/tokengen/main.go          # JWT token üretici CLI
├── docker-compose.yml
├── Dockerfile.gateway
└── Makefile
```

---

## Tasarım Kararları

### Neden Worker Pool?
Go goroutine'leri ucuzdur ama sınırsız spawn üç soruna yol açar: tahmin edilemez bellek, Redis connection pool tükenmesi, scheduler overhead. Worker Pool bu üçünü de çözer ve backpressure mekanizması sağlar.

### Neden Circuit Breaker?
Analytics engine yavaşladığında Redis yazmaları bloklanır. Blok → HTTP handler bekler → gateway de yavaşlar → cascade failure. CB ile Redis çöktüğünde gateway 3ms'de hata döner ve çalışmaya devam eder.

### Neden Redis Stream (List değil)?
Stream'in consumer group'ları var: birden fazla analytics instance paralel tüketebilir. XACK ile at-least-once delivery garantisi. List'te bu özellikler yoktur.

### Neden Pure stdlib Z-Score (NumPy değil)?
Edge node'larında (endüstriyel gateway, Raspberry Pi) NumPy 30-80MB RAM tutar. `collections.deque` + `math.sqrt` ile aynı hesaplama <1MB'da çalışır. 60 elemanlı pencere için NumPy'nin vektörizasyon avantajı ihmal edilebilir.

# AeolusEdge Makefile
# Kullanım: make <hedef>
# Tüm hedefler: make help

.PHONY: help build run test test-go test-py lint clean docker-up docker-down token

# Varsayılan JWT secret (sadece geliştirme)
JWT_SECRET ?= supersecretkey-change-in-production-env
export JWT_SECRET

# ── Yardım ───────────────────────────────────────────────────────────────────
help:
	@echo ""
	@echo "AeolusEdge — Kullanılabilir Komutlar"
	@echo "════════════════════════════════════════"
	@echo "  make build       → Go gateway'i derle"
	@echo "  make run         → Gateway'i yerel çalıştır (Redis gerekli)"
	@echo "  make test        → Tüm testleri çalıştır (Go + Python)"
	@echo "  make test-go     → Sadece Go testleri (race detector ile)"
	@echo "  make test-py     → Sadece Python testleri"
	@echo "  make lint        → Go lint kontrolü"
	@echo "  make token       → Test JWT token üret"
	@echo "  make docker-up   → Tüm servisleri Docker ile başlat"
	@echo "  make docker-down → Docker servislerini durdur"
	@echo "  make clean       → Build artifact'larını temizle"
	@echo "  make simulate    → Turbine simülatörünü yerel çalıştır"
	@echo "  make analytics   → Analytics engine'i yerel çalıştır"
	@echo ""

# ── Go Build ─────────────────────────────────────────────────────────────────
build:
	@echo "→ Gateway derleniyor..."
	CGO_ENABLED=0 go build \
		-ldflags="-s -w" \
		-o ./bin/gateway \
		./cmd/gateway
	@echo "✓ ./bin/gateway hazır"

# ── Çalıştırma ───────────────────────────────────────────────────────────────
run: build
	@echo "→ Gateway başlatılıyor (Redis: localhost:6379)..."
	./bin/gateway

# ── Testler ──────────────────────────────────────────────────────────────────
test: test-go test-py
	@echo ""
	@echo "✓ Tüm testler tamamlandı"

test-go:
	@echo "→ Go testleri çalışıyor (race detector açık)..."
	go test -v -race -count=1 ./...
	@echo "✓ Go testleri başarılı"

test-py:
	@echo "→ Python testleri çalışıyor..."
	cd simulator && python test_analytics.py
	@echo "✓ Python testleri başarılı"

test-coverage:
	@echo "→ Go test coverage raporu..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓ coverage.html oluşturuldu"

# ── Lint ─────────────────────────────────────────────────────────────────────
lint:
	@echo "→ Go lint kontrolü..."
	go vet ./...
	@echo "✓ go vet geçti"
	@which staticcheck > /dev/null 2>&1 && staticcheck ./... || \
		echo "  (staticcheck kurulu değil: go install honnef.co/go/tools/cmd/staticcheck@latest)"

# ── Token Üretimi ─────────────────────────────────────────────────────────────
token:
	@echo "→ Test token üretiliyor (turbine-001)..."
	JWT_SECRET=$(JWT_SECRET) go run ./tools/tokengen \
		--device turbine-001 \
		--ttl 24h

# ── Docker ───────────────────────────────────────────────────────────────────
docker-up:
	@echo "→ Tüm servisler başlatılıyor..."
	docker compose up --build -d
	@echo ""
	@echo "✓ Servisler başlatıldı:"
	@echo "  Gateway  → http://localhost:8080"
	@echo "  Health   → http://localhost:8080/health"
	@echo "  Metrics  → http://localhost:8080/metrics"
	@echo "  Stats    → http://localhost:8080/stats"
	@echo "  WebSocket→ ws://localhost:8080/ws"
	@echo ""
	@echo "  Log izle: docker compose logs -f"

docker-down:
	docker compose down
	@echo "✓ Servisler durduruldu"

docker-logs:
	docker compose logs -f

# ── Yerel Geliştirme ─────────────────────────────────────────────────────────
redis-start:
	@echo "→ Redis başlatılıyor..."
	redis-server --daemonize yes --logfile /tmp/redis-aeolus.log
	@echo "✓ Redis çalışıyor (log: /tmp/redis-aeolus.log)"

redis-stop:
	redis-cli shutdown nosave 2>/dev/null || true

simulate:
	@echo "→ Turbine simülatörü başlatılıyor..."
	cd simulator && python turbine_simulator.py

analytics:
	@echo "→ Analytics engine başlatılıyor..."
	cd simulator && python analytics_engine.py

install-py:
	@echo "→ Python bağımlılıkları kuruluyor..."
	pip install -r simulator/requirements.txt
	@echo "✓ Python bağımlılıkları kuruldu"

# ── Temizlik ─────────────────────────────────────────────────────────────────
clean:
	rm -rf ./bin coverage.out coverage.html
	find . -name "__pycache__" -type d -exec rm -rf {} + 2>/dev/null || true
	find . -name "*.pyc" -delete 2>/dev/null || true
	@echo "✓ Temizlendi"

# ── Hızlı Demo (tek komut) ───────────────────────────────────────────────────
demo: docker-up
	@echo ""
	@echo "🌬  AeolusEdge Demo Başladı"
	@echo "════════════════════════════"
	@sleep 5
	@echo "→ Health kontrol:"
	@curl -s http://localhost:8080/health | python3 -m json.tool || echo "  (henüz hazır değil, birkaç saniye bekleyin)"
	@echo ""
	@echo "→ Log izlemek için: docker compose logs -f analytics"
	@echo "→ WebSocket test: wscat -c ws://localhost:8080/ws"

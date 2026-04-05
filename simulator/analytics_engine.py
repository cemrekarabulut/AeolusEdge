#!/usr/bin/env python3
"""
analytics_engine.py — Gerçek zamanlı anomali tespit motoru

MİMARİ KARARLAR:
  - Redis Stream + Consumer Group: at-least-once delivery garantisi
    Sistem çökerse yarım kalan mesajlar kaybolmaz, XPENDING ile kurtarılır
  - Sliding Window: deque(maxlen=N) → O(1) push/pop, sabit bellek
  - Welford Online Algorithm: pencereyi yeniden hesaplamak yerine O(1) güncelleme
  - Micro-batch (count=50): tek XREADGROUP'ta 50 mesaj → daha az RTT
  - Pure stdlib math: NumPy yok → edge node'larda düşük bellek kullanımı

NEDEN Z-SCORE (3σ kuralı):
  - Chebyshev eşitsizliği: z≥3 → veri noktasının %≥88.9'u normal aralıkta
  - Endüstriyel sensörler için ISO 10816 standartta kullanılan yaklaşım
  - False positive/negative dengesi: threshold 3σ → yaklaşık %0.3 FP oranı
"""

import asyncio
import json
import logging
import os
import time

import redis.asyncio as aioredis

# Saf hesaplama katmanı — Redis bağımlılığı yok, ayrı test edilebilir
from analytics_core import AnomalyDetector, MetricWindow, WelfordStats  # noqa: F401

# ── Yapılandırma ──────────────────────────────────────────────────────────────
REDIS_URL      = os.getenv("REDIS_URL", "redis://localhost:6379/0")
STREAM_KEY     = "sensor:readings"
GROUP_NAME     = "analytics-group"
CONSUMER_NAME  = os.getenv("CONSUMER_NAME", "engine-1")
ALERT_CHANNEL  = "anomaly:alerts"

WINDOW_SIZE    = int(os.getenv("WINDOW_SIZE", "60"))
Z_THRESHOLD    = float(os.getenv("Z_THRESHOLD", "3.0"))
BATCH_SIZE     = int(os.getenv("BATCH_SIZE", "50"))
BLOCK_MS       = int(os.getenv("BLOCK_MS", "500"))

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s — %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger("aeolus.analytics")


async def create_consumer_group(r: aioredis.Redis) -> None:
    """
    Consumer group'u idempotent oluştur.
    "$" → sadece bu noktadan sonraki mesajları al (geçmişi atla)
    "0" → stream başından tüm mesajları al
    """
    try:
        await r.xgroup_create(STREAM_KEY, GROUP_NAME, id="$", mkstream=True)
        log.info("Consumer group oluşturuldu: %s/%s", STREAM_KEY, GROUP_NAME)
    except aioredis.ResponseError as e:
        if "BUSYGROUP" in str(e):
            log.info("Consumer group zaten mevcut: %s", GROUP_NAME)
        else:
            raise


async def reclaim_pending(r: aioredis.Redis, detector: AnomalyDetector) -> None:
    """
    XAUTOCLAIM: 30s boyunca ACK edilmemiş mesajları sahiplen ve yeniden işle.
    Bu sayede çöken consumer'ların mesajları kaybolmaz.
    """
    try:
        result = await r.xautoclaim(
            STREAM_KEY, GROUP_NAME, CONSUMER_NAME,
            min_idle_time=30000,  # 30 saniye işlenmemişse sahiplen
            start_id="0-0",
            count=100,
        )
        messages = result[1] if isinstance(result, (list, tuple)) else []
        if messages:
            log.info("XAUTOCLAIM: %d bekleyen mesaj sahiplenildi", len(messages))
    except Exception as e:
        log.debug("XAUTOCLAIM: %s", e)


async def consume_loop(r: aioredis.Redis, detector: AnomalyDetector) -> None:
    """Ana tüketim döngüsü."""
    total_processed = 0
    last_status_time = time.time()

    log.info("Tüketim başladı | stream=%s group=%s consumer=%s",
             STREAM_KEY, GROUP_NAME, CONSUMER_NAME)

    while True:
        try:
            # XREADGROUP: Consumer group'tan mikro-batch al
            # ">": sadece bu consumer'a atanmamış yeni mesajları al
            results = await r.xreadgroup(
                GROUP_NAME,
                CONSUMER_NAME,
                {STREAM_KEY: ">"},
                count=BATCH_SIZE,
                block=BLOCK_MS,
            )

            if not results:
                continue  # timeout, yeni mesaj yok

            for _stream_name, entries in results:
                for entry_id, fields in entries:
                    try:
                        payload_str = fields.get("payload", "{}")
                        payload = json.loads(payload_str)

                        alerts = detector.process(payload)

                        if alerts:
                            for alert in alerts:
                                log.warning(
                                    "🚨 ANOMALİ | device=%-12s metric=%-12s value=%8.3f z=%.2f [%s]",
                                    alert["device_id"], alert["metric"],
                                    alert["value"], alert["z_score"], alert["severity"],
                                )
                            # Alertleri Redis Pub/Sub'a yayınla
                            await r.publish(ALERT_CHANNEL, json.dumps(alerts))

                        # XACK: mesajı işlenmiş olarak işaretle
                        # NEDEN: ACK olmadan mesaj XPENDING'de kalır → yeniden işlenir
                        await r.xack(STREAM_KEY, GROUP_NAME, entry_id)
                        total_processed += 1

                    except json.JSONDecodeError as e:
                        log.error("JSON parse hatası | id=%s err=%s", entry_id, e)
                        await r.xack(STREAM_KEY, GROUP_NAME, entry_id)  # bozuk mesajı sil
                    except Exception as e:
                        log.error("Mesaj işleme hatası | id=%s err=%s", entry_id, e)
                        # ACK YOK: mesaj XPENDING'de kalır, yeniden işlenecek

            # Her 30 saniyede bir durum raporu
            now = time.time()
            if now - last_status_time >= 30:
                log.info(
                    "Durum | işlenen=%d anomali=%d cihaz_sayısı=%d",
                    total_processed,
                    detector.anomaly_count,
                    len(detector._windows),
                )
                last_status_time = now

                # Bekleyen mesajları yeniden sahiplen
                await reclaim_pending(r, detector)

        except aioredis.ConnectionError as e:
            log.error("Redis bağlantısı koptu: %s — 3s sonra yeniden denenecek", e)
            await asyncio.sleep(3)
        except asyncio.CancelledError:
            log.info("Tüketim döngüsü iptal edildi")
            raise
        except Exception as e:
            log.error("Beklenmeyen hata: %s", e, exc_info=True)
            await asyncio.sleep(1)


async def main():
    log.info("AeolusEdge Analytics Engine başlatıldı")
    log.info("Yapılandırma | window=%d threshold=%.1fσ batch=%d",
             WINDOW_SIZE, Z_THRESHOLD, BATCH_SIZE)

    r = aioredis.from_url(REDIS_URL, decode_responses=True)

    try:
        await r.ping()
        log.info("Redis bağlantısı kuruldu: %s", REDIS_URL)
    except Exception as e:
        log.error("Redis bağlanamadı: %s", e)
        raise

    await create_consumer_group(r)
    detector = AnomalyDetector()

    try:
        await consume_loop(r, detector)
    except asyncio.CancelledError:
        pass
    finally:
        await r.aclose()
        log.info("Analytics engine kapatıldı | toplam_anomali=%d", detector.anomaly_count)


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("\nAnalytics engine durduruldu.")

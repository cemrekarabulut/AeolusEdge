#!/usr/bin/env python3
"""
turbine_simulator.py — Rüzgar türbini veri simülatörü

NEDEN ASYNCIO:
  - 10+ türbini aynı anda simüle etmek için thread değil coroutine kullanıyoruz
  - httpx.AsyncClient: thread-safe, bağlantı havuzlama, HTTP/2 desteği
  - asyncio.gather: 10 türbin eş zamanlı çalışır, sıralı değil

ANOMALİ TİPLERİ:
  - Normal: Gaussian gürültü (μ±σ), gerçek sensör davranışı
  - Spike: Anlık 4-6σ sapma — rulman hasarı, elektrik arızası simülasyonu
  - Drift: Kademeli artış — sensör kalibrasyonu bozulması, aşınma simülasyonu
"""

import asyncio
import json
import logging
import math
import os
import random
import time

import httpx
import numpy as np

# ── Yapılandırma ──────────────────────────────────────────────────────────────
GATEWAY_URL   = os.getenv("GATEWAY_URL", "http://localhost:8080/ingest")
JWT_SECRET    = os.getenv("JWT_SECRET", "supersecretkey-change-in-production-env")
TURBINE_COUNT = int(os.getenv("TURBINE_COUNT", "5"))
SEND_INTERVAL = float(os.getenv("SEND_INTERVAL_S", "0.5"))  # saniye/okuma

# Anomali olasılıkları — gerçek rüzgar çiftliği istatistiklerine yakın
SPIKE_PROB = 0.03   # %3: anlık spike
DRIFT_PROB = 0.08   # %8: drift başlangıcı

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s — %(message)s",
    datefmt="%H:%M:%S",
)


class TurbineSimulator:
    """
    Tek bir türbin simülatörü.

    Her instance kendi drift state'ini tutar — türbinler birbirinden bağımsız.
    _generate() saf fonksiyon gibi tasarlandı (sadece self.drift_offset yan etki).
    """

    # Normal operasyon parametreleri (gerçek değerlere yakın)
    PARAMS = {
        "vibration":   {"mean": 5.0,  "std": 0.3},   # mm/s
        "rpm":         {"mean": 1500, "std": 60},     # dev/dak
        "temperature": {"mean": 72,   "std": 3},      # °C
    }

    def __init__(self, device_id: str, token: str):
        self.device_id    = device_id
        self.token        = token
        self.drift_offset = 0.0
        self.in_drift     = False
        self.log          = logging.getLogger(f"turbine.{device_id}")

    def _generate(self) -> dict:
        """
        Sensör okuması üretir.
        Dönen dict: doğrudan JSON serileştirilebilir.
        """
        r = random.random()

        if r < SPIKE_PROB:
            # ── Spike Anomali ─────────────────────────────────────────────
            # Anlık 4-6σ sapma. Gerçek dünyada: yıldırım çarpması, kısa devre.
            multiplier = random.uniform(4, 6)
            readings = {
                "vibration":   round(float(np.random.normal(
                    self.PARAMS["vibration"]["mean"] + multiplier * self.PARAMS["vibration"]["std"],
                    self.PARAMS["vibration"]["std"] * 0.5,
                )), 4),
                "rpm":         round(float(np.random.normal(
                    self.PARAMS["rpm"]["mean"] + multiplier * 100,
                    self.PARAMS["rpm"]["std"],
                )), 2),
                "temperature": round(float(np.random.normal(
                    self.PARAMS["temperature"]["mean"] + multiplier * 5,
                    self.PARAMS["temperature"]["std"],
                )), 2),
            }
            self.log.warning("⚡ SPIKE anomali üretildi: vib=%.2f rpm=%.0f",
                           readings["vibration"], readings["rpm"])

        elif r < SPIKE_PROB + DRIFT_PROB and not self.in_drift:
            # ── Drift Başlangıcı ─────────────────────────────────────────
            # Kademeli sapma. Gerçek dünyada: rulman yağı azalması, titreşim artışı.
            self.in_drift = True
            self.drift_offset = 0.0
            self.log.info("〰 DRIFT anomali başladı")
            readings = self._normal_readings()

        else:
            # ── Normal Operasyon ─────────────────────────────────────────
            if self.in_drift:
                # Drift devam ediyor
                self.drift_offset = min(self.drift_offset + 0.08, 3.0)
                readings = {
                    "vibration":   round(float(np.random.normal(
                        self.PARAMS["vibration"]["mean"] + self.drift_offset,
                        self.PARAMS["vibration"]["std"],
                    )), 4),
                    "rpm":         round(float(np.random.normal(
                        self.PARAMS["rpm"]["mean"] + self.drift_offset * 20,
                        self.PARAMS["rpm"]["std"],
                    )), 2),
                    "temperature": round(float(np.random.normal(
                        self.PARAMS["temperature"]["mean"] + self.drift_offset * 2,
                        self.PARAMS["temperature"]["std"],
                    )), 2),
                }
                # Drift 60 okuma sonra kendiliğinden düzelir
                if self.drift_offset >= 2.8:
                    self.in_drift = False
                    self.drift_offset = 0.0
                    self.log.info("〰 DRIFT anomali bitti")
            else:
                readings = self._normal_readings()

        return {
            "device_id":   self.device_id,
            "timestamp":   time.time(),
            **readings,
        }

    def _normal_readings(self) -> dict:
        """Gürültülü ama normal aralıkta okuma üretir."""
        return {
            "vibration":   round(float(np.random.normal(
                self.PARAMS["vibration"]["mean"],
                self.PARAMS["vibration"]["std"],
            )), 4),
            "rpm":         round(float(np.random.normal(
                self.PARAMS["rpm"]["mean"],
                self.PARAMS["rpm"]["std"],
            )), 2),
            "temperature": round(float(np.random.normal(
                self.PARAMS["temperature"]["mean"],
                self.PARAMS["temperature"]["std"],
            )), 2),
        }

    async def run(self, client: httpx.AsyncClient) -> None:
        """Sonsuz döngü: üret → gönder → bekle."""
        self.log.info("başlatıldı → %s", GATEWAY_URL)
        consecutive_errors = 0

        while True:
            payload = self._generate()
            try:
                resp = await client.post(
                    GATEWAY_URL,
                    json=payload,
                    headers={"Authorization": f"Bearer {self.token}"},
                    timeout=3.0,
                )
                if resp.status_code == 202:
                    consecutive_errors = 0
                    self.log.debug("gönderildi: vib=%.3f rpm=%.0f temp=%.1f",
                                  payload["vibration"], payload["rpm"], payload["temperature"])
                else:
                    self.log.warning("beklenmeyen status: %d body=%s",
                                   resp.status_code, resp.text[:100])
                    consecutive_errors += 1

            except httpx.TimeoutException:
                self.log.error("timeout — gateway yanıt vermiyor")
                consecutive_errors += 1
            except httpx.ConnectError:
                self.log.error("bağlantı hatası — gateway çalışıyor mu? (%s)", GATEWAY_URL)
                consecutive_errors += 1
            except Exception as e:
                self.log.error("beklenmeyen hata: %s", e)
                consecutive_errors += 1

            # Exponential backoff: 5 ardışık hata sonrası yavaşla
            if consecutive_errors > 5:
                delay = min(SEND_INTERVAL * (2 ** (consecutive_errors - 5)), 30.0)
                self.log.warning("backoff: %.1fs bekleniyor (ardışık hata: %d)", delay, consecutive_errors)
                await asyncio.sleep(delay)
            else:
                await asyncio.sleep(SEND_INTERVAL)


def generate_token(device_id: str, secret: str) -> str:
    """
    Basit JWT üretici — production'da Go tokengen tool'u kullan.
    Bu sadece simülatörün kendi kendine çalışabilmesi için.
    """
    import base64
    import hashlib
    import hmac

    header  = base64.urlsafe_b64encode(b'{"alg":"HS256","typ":"JWT"}').rstrip(b'=').decode()
    payload_data = json.dumps({
        "device_id": device_id,
        "sub": device_id,
        "iss": "aeolus-edge",
        "iat": int(time.time()),
        "exp": int(time.time()) + 86400 * 7,  # 7 gün
    }).encode()
    payload = base64.urlsafe_b64encode(payload_data).rstrip(b'=').decode()

    message   = f"{header}.{payload}".encode()
    signature = hmac.new(secret.encode(), message, hashlib.sha256).digest()
    sig_b64   = base64.urlsafe_b64encode(signature).rstrip(b'=').decode()

    return f"{header}.{payload}.{sig_b64}"


async def main():
    """10 türbini paralel başlatır."""
    secret = JWT_SECRET

    turbines = [
        TurbineSimulator(
            device_id=f"turbine-{i:03d}",
            token=generate_token(f"turbine-{i:03d}", secret),
        )
        for i in range(1, TURBINE_COUNT + 1)
    ]

    # Tek AsyncClient tüm turbineler için — bağlantı havuzlaması
    async with httpx.AsyncClient(
        limits=httpx.Limits(max_connections=TURBINE_COUNT + 4),
    ) as client:
        print(f"🌬  {TURBINE_COUNT} türbin simülatörü başlatıldı → {GATEWAY_URL}")
        print(f"   Spike olasılığı: {SPIKE_PROB*100:.0f}%  Drift olasılığı: {DRIFT_PROB*100:.0f}%")
        print(f"   Gönderim aralığı: {SEND_INTERVAL}s/türbin")
        print("   Durdurmak için Ctrl+C\n")

        await asyncio.gather(*(t.run(client) for t in turbines))


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("\n\nSimülatör durduruldu.")

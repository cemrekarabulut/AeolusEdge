"""
analytics_core.py — Saf hesaplama katmanı (dış bağımlılık YOK)

İYİLEŞTİRMELER (v2):
  1. MAD (Median Absolute Deviation) eklendi — outlier'lara robust, Z-score'dan üstün
  2. device_count property düzeltildi
  3. evict_device() eklendi — uzun süre gözlemlenmeyen cihazları bellekten temizler
  4. window_full_count() — kaç cihaz warm-up'ı tamamladı
"""

import math
import time
from collections import deque
from dataclasses import dataclass, field
from typing import Optional

DEFAULT_WINDOW_SIZE = 60
DEFAULT_Z_THRESHOLD = 3.0


@dataclass
class WelfordStats:
    """Welford Online Algorithm — O(1) kayan ortalama ve varyans."""
    n:    int   = 0
    mean: float = 0.0
    M2:   float = 0.0

    def update(self, x: float) -> None:
        self.n += 1
        delta = x - self.mean
        self.mean += delta / self.n
        self.M2 += delta * (x - self.mean)

    @property
    def variance(self) -> float:
        return self.M2 / self.n if self.n > 1 else 0.0

    @property
    def std(self) -> float:
        return math.sqrt(self.variance)


@dataclass
class MetricWindow:
    """
    Tek bir metrik için sliding window.

    İYİLEŞTİRME (v2): mad_score() eklendi.
    MAD (Median Absolute Deviation): outlier'lara Z-score'dan çok daha robust.
    Gerçek dünya sensör verisi çarpık dağılım gösterir — MAD bu durumda daha güvenilir.
    """
    size:   int   = DEFAULT_WINDOW_SIZE
    values: deque = field(default_factory=deque)

    def __post_init__(self):
        self.values = deque(maxlen=self.size)

    def push(self, v: float) -> None:
        self.values.append(v)

    @property
    def is_ready(self) -> bool:
        return len(self.values) >= self.size

    def z_score(self, v: float) -> Optional[float]:
        """
        Peek-then-push: değer pencereye EKLEMEden hesaplanır.
        Anomali değeri pencereye girerse istatistikleri bozar → tespit kaçar.
        """
        if not self.is_ready:
            return None
        n    = len(self.values)
        mean = sum(self.values) / n
        var  = sum((x - mean) ** 2 for x in self.values) / n
        std  = math.sqrt(var) if var > 0 else 0.0
        return abs(v - mean) / std if std > 0 else 0.0

    def mad_score(self, v: float) -> Optional[float]:
        """
        MAD (Median Absolute Deviation) skoru — Z-score'a alternatif.

        NEDEN MAD:
          Z-score outlier'lardan etkilenir (outlier μ ve σ'yı bozar).
          MAD medyan tabanlıdır — outlier'lara karşı robust.
          Endüstriyel sensörlerde aralıklı spike'lar Z-score'u köreltir.

        mad_score = |v - median| / (1.4826 * MAD)
        1.4826: normal dağılım için ölçekleme faktörü (Z-score ile karşılaştırılabilir)

        None → warm-up
        """
        if not self.is_ready:
            return None
        sorted_vals = sorted(self.values)
        n = len(sorted_vals)
        # Medyan
        if n % 2 == 1:
            median = sorted_vals[n // 2]
        else:
            median = (sorted_vals[n // 2 - 1] + sorted_vals[n // 2]) / 2.0
        # MAD
        deviations = sorted([abs(x - median) for x in sorted_vals])
        if n % 2 == 1:
            mad = deviations[n // 2]
        else:
            mad = (deviations[n // 2 - 1] + deviations[n // 2]) / 2.0

        if mad == 0:
            return 0.0
        return abs(v - median) / (1.4826 * mad)

    def stats(self) -> dict:
        if not self.values:
            return {"mean": 0.0, "std": 0.0, "n": 0, "ready": False}
        n    = len(self.values)
        mean = sum(self.values) / n
        var  = sum((x - mean) ** 2 for x in self.values) / n
        return {
            "mean":  round(mean, 4),
            "std":   round(math.sqrt(var), 4),
            "n":     n,
            "ready": self.is_ready,
        }


class AnomalyDetector:
    """
    Tüm cihazlar × metrikler için anomali tespiti.

    İYİLEŞTİRME (v2):
      - use_mad parametresi: Z-score veya MAD seçilebilir
      - evict_device(): eski cihazları bellekten temizler
      - _last_seen: cihaz bazlı son görülme zamanı
    """

    METRICS = ("vibration", "rpm", "temperature")

    def __init__(
        self,
        window_size: int   = DEFAULT_WINDOW_SIZE,
        threshold:   float = DEFAULT_Z_THRESHOLD,
        use_mad:     bool  = False,
    ) -> None:
        self.window_size = window_size
        self.threshold   = threshold
        self.use_mad     = use_mad
        self._windows:    dict[str, dict[str, MetricWindow]] = {}
        self._last_seen:  dict[str, float] = {}
        self._anomaly_count = 0

    def _get_window(self, device_id: str, metric: str) -> MetricWindow:
        if device_id not in self._windows:
            self._windows[device_id] = {
                m: MetricWindow(size=self.window_size)
                for m in self.METRICS
            }
        return self._windows[device_id][metric]

    def process(self, payload: dict) -> list[dict]:
        device_id = payload.get("device_id", "unknown")
        timestamp = payload.get("timestamp", time.time())
        self._last_seen[device_id] = timestamp
        alerts = []

        for metric in self.METRICS:
            value = payload.get(metric)
            if value is None:
                continue

            window = self._get_window(device_id, metric)

            # Peek: önce hesapla, sonra push
            if self.use_mad:
                score = window.mad_score(value)
            else:
                score = window.z_score(value)
            window.push(value)

            if score is None:
                continue  # warm-up

            if score >= self.threshold:
                stats    = window.stats()
                severity = self._classify(score)
                alert = {
                    "device_id":  device_id,
                    "metric":     metric,
                    "value":      round(float(value), 4),
                    "z_score":    round(score, 3),
                    "score_type": "mad" if self.use_mad else "zscore",
                    "mean":       stats["mean"],
                    "std_dev":    stats["std"],
                    "severity":   severity,
                    "timestamp":  timestamp,
                }
                alerts.append(alert)
                self._anomaly_count += 1

        return alerts

    @staticmethod
    def _classify(z: float) -> str:
        if z >= 5.0:   return "CRITICAL"
        elif z >= 4.0: return "ERROR"
        return "WARNING"

    @property
    def anomaly_count(self) -> int:
        return self._anomaly_count

    @property
    def device_count(self) -> int:
        return len(self._windows)

    def evict_device(self, max_age_seconds: float = 3600.0) -> list[str]:
        """
        Son max_age_seconds'dan fazla görülmeyen cihazları bellekten temizle.
        Edge node'da uzun süre çalışmada bellek sızıntısını önler.
        Döndürür: temizlenen device_id listesi
        """
        now = time.time()
        evicted = [
            did for did, last in self._last_seen.items()
            if now - last > max_age_seconds
        ]
        for did in evicted:
            del self._windows[did]
            del self._last_seen[did]
        return evicted

    def warmup_status(self) -> dict[str, bool]:
        """Her cihazın warm-up tamamlayıp tamamlamadığını döndürür."""
        return {
            did: all(w.is_ready for w in wins.values())
            for did, wins in self._windows.items()
        }

    def window_status(self) -> dict:
        return {
            did: {m: w.stats() for m, w in wins.items()}
            for did, wins in self._windows.items()
        }

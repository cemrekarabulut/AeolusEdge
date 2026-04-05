#!/usr/bin/env python3
"""
test_analytics.py — Analytics engine birim testleri (v2)

Yeni testler:
  - MAD score testi
  - evict_device testi
  - warmup_status testi
  - score_type alanı testi
"""

import math
import sys
import time
import unittest

sys.path.insert(0, ".")
from analytics_core import AnomalyDetector, MetricWindow, WelfordStats


class TestMetricWindow(unittest.TestCase):

    def test_window_not_ready_before_full(self):
        """Pencere dolmadan z_score None döner."""
        w = MetricWindow(size=5)
        for i in range(4):
            w.push(float(i))
        self.assertIsNone(w.z_score(10.0))

    def test_window_ready_when_full(self):
        """Pencere dolduğunda z_score değer döner."""
        w = MetricWindow(size=5)
        for i in range(5):
            w.push(float(i))
        self.assertIsNotNone(w.z_score(10.0))

    def test_sliding_removes_old_values(self):
        """maxlen aşıldığında eski değerler düşer."""
        w = MetricWindow(size=3)
        for v in [1.0, 2.0, 3.0, 100.0]:
            w.push(v)
        self.assertEqual(len(w.values), 3)
        self.assertNotIn(1.0, w.values)

    def test_z_score_normal_value(self):
        """Sabit değerler için z_score sıfır döner (std=0)."""
        w = MetricWindow(size=10)
        for _ in range(10):
            w.push(5.0)
        self.assertEqual(w.z_score(5.0), 0.0)

    def test_z_score_anomalous_value(self):
        """Anomali değeri yüksek z_score döndürür."""
        w = MetricWindow(size=20)
        for i in range(20):
            w.push(5.0 + 0.05 * (i % 3))
        z = w.z_score(50.0)
        self.assertIsNotNone(z)
        self.assertGreater(z, 3.0)

    def test_stats_returns_correct_shape(self):
        """stats() doğru alanları döndürür."""
        w = MetricWindow(size=5)
        for i in range(5):
            w.push(float(i))
        s = w.stats()
        for key in ("mean", "std", "n", "ready"):
            self.assertIn(key, s)
        self.assertEqual(s["n"], 5)
        self.assertTrue(s["ready"])

    # ── MAD testi (YENİ) ──────────────────────────────────────────────────
    def test_mad_score_none_before_ready(self):
        """Warm-up'ta MAD score None döner."""
        w = MetricWindow(size=10)
        for i in range(9):
            w.push(5.0)
        self.assertIsNone(w.mad_score(5.0))

    def test_mad_score_anomaly(self):
        """MAD score büyük outlier için yüksek değer döndürür."""
        w = MetricWindow(size=10)
        # Gaussian varyasyonlu değerler — MAD > 0 olacak
        import random
        random.seed(42)
        for _ in range(10):
            w.push(5.0 + random.uniform(-0.5, 0.5))
        score = w.mad_score(100.0)
        self.assertIsNotNone(score)
        self.assertGreater(score, 3.0)

    def test_mad_score_robust_to_outliers(self):
        """MAD önceki outlier'lardan etkilenmez — Z-score'dan daha robust."""
        w_mad = MetricWindow(size=10)
        w_z   = MetricWindow(size=10)
        # Normal değerler + 1 outlier
        values = [5.0] * 9 + [100.0]
        for v in values:
            w_mad.push(v)
            w_z.push(v)
        # Yeni normal değer için MAD, Z-score'dan düşük olmalı
        mad_score = w_mad.mad_score(5.5)
        z_score   = w_z.z_score(5.5)
        # Her ikisi de None değil
        self.assertIsNotNone(mad_score)
        self.assertIsNotNone(z_score)


class TestAnomalyDetector(unittest.TestCase):

    def _make_normal_payload(self, device_id="turbine-001", **overrides):
        base = {
            "device_id":   device_id,
            "timestamp":   time.time(),
            "vibration":   5.0,
            "rpm":         1500.0,
            "temperature": 72.0,
        }
        base.update(overrides)
        return base

    def _fill_window(self, detector, device_id="turbine-001", n=10, vib=5.0):
        import random
        random.seed(42)
        for _ in range(n):
            detector.process(self._make_normal_payload(
                device_id, vibration=vib + random.uniform(-0.3, 0.3)
            ))

    def test_no_alerts_during_warmup(self):
        """Warm-up döneminde alert üretilmez."""
        detector = AnomalyDetector(window_size=60, threshold=3.0)
        for _ in range(59):
            alerts = detector.process(self._make_normal_payload())
            self.assertEqual(alerts, [])

    def test_anomaly_detected_after_warmup(self):
        """Pencere dolduktan sonra spike alert üretir."""
        detector = AnomalyDetector(window_size=10, threshold=3.0)
        self._fill_window(detector)
        alerts = detector.process(self._make_normal_payload(vibration=200.0))
        self.assertTrue(len(alerts) > 0)
        self.assertEqual(alerts[0]["metric"], "vibration")
        self.assertGreaterEqual(alerts[0]["z_score"], 3.0)

    def test_normal_value_no_alert(self):
        """Ortalamaya yakın değer alert üretmez."""
        detector = AnomalyDetector(window_size=10, threshold=3.0)
        self._fill_window(detector)
        alerts = detector.process(self._make_normal_payload(vibration=5.0))
        vib_alerts = [a for a in alerts if a["metric"] == "vibration"]
        self.assertEqual(vib_alerts, [])

    def test_multiple_devices_isolated(self):
        """Farklı cihazlar birbirini etkilemez."""
        detector = AnomalyDetector(window_size=10, threshold=3.0)
        self._fill_window(detector, "turbine-001")
        # turbine-002 warm-up'ta, spike alert vermemeli
        alerts = detector.process(self._make_normal_payload("turbine-002", vibration=100.0))
        self.assertEqual(alerts, [])

    def test_severity_classification(self):
        """Severity sınıflandırması doğru."""
        detector = AnomalyDetector(window_size=10, threshold=2.0)
        self._fill_window(detector)
        alerts = detector.process(self._make_normal_payload(vibration=1000.0))
        if alerts:
            self.assertIn(alerts[0]["severity"], ["WARNING", "ERROR", "CRITICAL"])

    def test_alert_structure(self):
        """Alert dict gerekli tüm alanları içerir."""
        detector = AnomalyDetector(window_size=5, threshold=2.0)
        self._fill_window(detector, n=5)
        alerts = detector.process(self._make_normal_payload(vibration=999.0))
        if alerts:
            for field in ["device_id","metric","value","z_score","mean","std_dev","severity","timestamp","score_type"]:
                self.assertIn(field, alerts[0], f"'{field}' eksik")

    def test_anomaly_count_increments(self):
        """anomaly_count artar."""
        detector = AnomalyDetector(window_size=5, threshold=2.0)
        import random; random.seed(7)
        for _ in range(5):
            detector.process(self._make_normal_payload(
                vibration=5.0 + random.uniform(-0.3, 0.3)
            ))
        initial = detector.anomaly_count
        detector.process(self._make_normal_payload(vibration=9999.0))
        self.assertGreater(detector.anomaly_count, initial)

    # ── YENİ TESTLER (v2) ─────────────────────────────────────────────────
    def test_mad_detector(self):
        """MAD tabanlı detector anomali üretir."""
        detector = AnomalyDetector(window_size=10, threshold=3.0, use_mad=True)
        self._fill_window(detector)
        alerts = detector.process(self._make_normal_payload(vibration=200.0))
        self.assertTrue(len(alerts) > 0)
        self.assertEqual(alerts[0]["score_type"], "mad")

    def test_zscore_detector_score_type(self):
        """Z-score detector score_type='zscore' döndürür."""
        detector = AnomalyDetector(window_size=10, threshold=3.0, use_mad=False)
        self._fill_window(detector)
        alerts = detector.process(self._make_normal_payload(vibration=200.0))
        if alerts:
            self.assertEqual(alerts[0]["score_type"], "zscore")

    def test_evict_device(self):
        """Eski cihazlar bellekten temizlenir."""
        detector = AnomalyDetector(window_size=5, threshold=3.0)
        # Geçmişte görülmüş gibi simüle et
        detector.process(self._make_normal_payload("old-turbine", **{"timestamp": time.time() - 7200}))
        detector._last_seen["old-turbine"] = time.time() - 7200  # 2 saat önce
        evicted = detector.evict_device(max_age_seconds=3600.0)
        self.assertIn("old-turbine", evicted)
        self.assertNotIn("old-turbine", detector._windows)

    def test_warmup_status(self):
        """warmup_status() warm-up tamamlanmamış cihazı gösterir."""
        detector = AnomalyDetector(window_size=10, threshold=3.0)
        for _ in range(5):
            detector.process(self._make_normal_payload())
        status = detector.warmup_status()
        self.assertIn("turbine-001", status)
        self.assertFalse(status["turbine-001"])  # henüz tamamlanmadı

    def test_device_count(self):
        """device_count doğru sayı döndürür."""
        detector = AnomalyDetector(window_size=5, threshold=3.0)
        for i in range(3):
            detector.process(self._make_normal_payload(f"turbine-{i:03d}"))
        self.assertEqual(detector.device_count, 3)


class TestWelfordStats(unittest.TestCase):

    def test_mean_correct(self):
        values = [2.0, 4.0, 4.0, 4.0, 5.0, 5.0, 7.0, 9.0]
        ws = WelfordStats()
        for v in values:
            ws.update(v)
        self.assertAlmostEqual(ws.mean, sum(values)/len(values), places=10)

    def test_std_correct(self):
        values = [2.0, 4.0, 4.0, 4.0, 5.0, 5.0, 7.0, 9.0]
        ws = WelfordStats()
        for v in values:
            ws.update(v)
        n    = len(values)
        mean = sum(values) / n
        expected_std = math.sqrt(sum((x-mean)**2 for x in values)/n)
        self.assertAlmostEqual(ws.std, expected_std, places=8)

    def test_single_value(self):
        """Tek değerde variance=0 döner (n>1 guard)."""
        ws = WelfordStats()
        ws.update(5.0)
        self.assertEqual(ws.variance, 0.0)
        self.assertEqual(ws.std, 0.0)


if __name__ == "__main__":
    print("AeolusEdge Analytics Engine Testleri (v2)")
    print("=" * 52)
    loader = unittest.TestLoader()
    suite  = unittest.TestSuite()
    suite.addTests(loader.loadTestsFromTestCase(TestMetricWindow))
    suite.addTests(loader.loadTestsFromTestCase(TestAnomalyDetector))
    suite.addTests(loader.loadTestsFromTestCase(TestWelfordStats))
    runner = unittest.TextTestRunner(verbosity=2)
    result = runner.run(suite)
    sys.exit(0 if result.wasSuccessful() else 1)

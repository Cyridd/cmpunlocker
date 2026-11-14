#!/usr/bin/env python3
import argparse
import csv
import json
from pathlib import Path
import tempfile
import unittest

import bench_linux as bench


class CollectionGuards(unittest.TestCase):
    def test_requires_manual_stock_load_before_creating_output(self):
        with self.assertRaisesRegex(ValueError, "stock-loaded"):
            bench.collect(argparse.Namespace(smoke=False, stock_loaded=False))

    def test_physical_override_allows_original_cached_identity(self):
        data = {"config_pci_identity": "1f4b10de", "bar0_xve_identity": "1f4b10de",
                "cached_pci_identity": "1f0b10de", "boot0": "166000a1"}
        bench.check_identity(data, "1f4b")
        data["bar0_xve_identity"] = "1f0b10de"
        with self.assertRaises(ValueError):
            bench.check_identity(data, "1f4b")

    def test_stock_policy_rejects_patched_original_id(self):
        log = "RM_POLICY command=20810107 status=00000000 response=00 00\n"
        bench.check_policy(log, "1f4b")
        with self.assertRaises(ValueError):
            bench.check_policy(log, "1f0b")

    def test_missing_or_conflicting_policy_fails(self):
        with self.assertRaises(ValueError):
            bench.check_policy("", "1f4b")
        log = "RM_POLICY command=20810107 status=00000000 response={}\n"
        with self.assertRaises(ValueError):
            bench.check_policy(log.format("00 00") + log.format("01 01"), "1f4b")

    def test_clpeak_uses_its_own_indices_and_known_alias_count(self):
        listing = "Vulkan Device 3: unknown device [Discrete GPU]\nVulkan Device 4: unknown device [Discrete GPU]\n"
        self.assertEqual(bench.clpeak_index(listing, "unknown device", 2), 3)
        with self.assertRaises(ValueError):
            bench.clpeak_index(listing, "unknown device", 1)

    def test_raw_output_cannot_leave_ignored_artifacts(self):
        with self.assertRaises(ValueError):
            bench.output_directory(bench.ROOT / "benchmark-raw-output")

    def test_cuda_discovery_failure_does_not_fall_back_to_vulkan_index(self):
        target = {"physical_index": 0, "device_name": "unknown device"}
        with self.assertRaisesRegex(bench.CudaDiscoveryUnavailable, "discovery failed before clpeak"):
            bench.clpeak_selection("CUDA", target, {}, "CUDA: driver init failed or no devices found")

    def test_cuda_selection_uses_cuda_name_and_driver_metadata(self):
        target = {"physical_index": 0, "device_name": "unknown device"}
        manifest = {"cuda": {"cuda_index": 2, "device_name": "CUDA GPU", "driver_api_version": 13050}}
        self.assertEqual(bench.clpeak_selection("CUDA", target, manifest, ""), (2, "CUDA GPU", "13.5"))


class MetricValidation(unittest.TestCase):
    def setUp(self):
        (bench.ROOT / "artifacts").mkdir(exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=bench.ROOT / "artifacts")
        self.root = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def test_rejects_clpeak_result_from_other_gpu(self):
        path = self.root / "clpeak.csv"
        path.write_text("backend,device,driver,status,test,metric,unit,value\nVulkan,other GPU,620.70.06,ok,fp32,float,gflops,100\n")
        with self.assertRaises(ValueError):
            bench.clpeak_metrics(path, "target GPU", "Vulkan", "620.70.06")

    def test_nonfinite_scores_are_rejected(self):
        path = self.root / "clpeak.csv"
        path.write_text("backend,device,driver,status,test,metric,unit,value\nCUDA,target GPU,620.70.06,ok,fp32,float,gflops,nan\n")
        with self.assertRaises(ValueError):
            bench.clpeak_metrics(path, "target GPU", "CUDA", "620.70.06")

    def test_clpeak_driver_labels_use_the_observed_api_encoding(self):
        raw = (620 << 22) | (70 << 14) | (6 << 6)
        self.assertEqual(bench.clpeak_driver_label("Vulkan", {"driver_version_raw": raw}), "620.280.384")
        self.assertEqual(bench.clpeak_driver_label("CUDA", {"driver_api_version": 13050}), "13.5")
        path = self.root / "clpeak.csv"
        path.write_text("backend,device,driver,status,test,metric,unit,value\nVulkan,target GPU,620.280.384,ok,fp32,float,gflops,100\n")
        self.assertEqual(bench.clpeak_metrics(path, "target GPU", "Vulkan", "620.280.384")["Vulkan/fp32/float/gflops"], 100)
        with self.assertRaises(ValueError):
            bench.clpeak_metrics(path, "target GPU", "Vulkan", "619.0.0")

    def write_vulkan(self, repeats):
        with (self.root / "test.csv").open("w") as f:
            writer = csv.writer(f)
            writer.writerow(["case", "repeat", "gpu_ms", "checked_pixels"])
            for repeat in repeats:
                writer.writerow(["draw_only", repeat, 2, 9])
        return {"workloads": [{"name": "test", "case": "draw_only"}], "repeats": 3, "jobs": {}}

    def test_missing_repeat_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "incomplete"):
            bench.metrics_for(self.root, self.write_vulkan([0, 1]))

    def test_duplicate_repeat_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "duplicate"):
            bench.metrics_for(self.root, self.write_vulkan([0, 1, 1]))

    def test_summary_preserves_samples_and_timestamp_floor(self):
        self.assertEqual(bench.summary([1, 2, 9])["median"], 2)
        self.assertEqual(bench.summary([0, 0, 0])["median"], 0)
        with self.assertRaises(ValueError):
            bench.summary([float("inf")])

    def test_comparison_rejects_smoke_evidence(self):
        paths = []
        for i, devid in enumerate(["1f0b", "1f4b", "1f0b"]):
            path = self.root / str(i)
            path.mkdir()
            bench.write_json(path / "run.json", {"device_id": devid, "mode": "smoke", "state": "complete"})
            paths.append(str(path))
        with self.assertRaisesRegex(ValueError, "stock"):
            bench.compare(argparse.Namespace(runs=paths, out=str(self.root / "comparison")))

    def test_comparison_reports_restored_baseline_drift(self):
        paths = []
        for i, (devid, value) in enumerate(zip(["1f0b", "1f4b", "1f0b"], [10, 2, 12])):
            path = self.root / str(i)
            path.mkdir()
            manifest = {"device_id": devid, "mode": "stock", "state": "complete", "repeats": 3,
                        "workloads": [{"name": "test", "case": "draw_only"}], "jobs": {}}
            bench.write_json(path / "run.json", manifest)
            (path / "test.csv").write_text("case,repeat,gpu_ms,checked_pixels\n" +
                "".join(f"draw_only,{rep},{value},9\n" for rep in range(3)))
            paths.append(str(path))
        out = self.root / "comparison"
        bench.compare(argparse.Namespace(runs=paths, out=str(out)))
        row = json.loads((out / "comparison.json").read_text())["rows"][0]
        self.assertEqual(row["B_speedup_vs_A1"], 5)
        self.assertEqual(row["B_speedup_vs_A2"], 6)
        self.assertAlmostEqual(row["A2_change_pct"], 20)


if __name__ == "__main__":
    unittest.main()

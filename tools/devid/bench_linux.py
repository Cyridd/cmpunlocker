#!/usr/bin/env python3
"""Collect manual stock-driver A/B/A runs; never change GPU or driver state."""
import argparse
import csv
import hashlib
import json
import math
import os
from pathlib import Path
import re
import statistics
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
STOCK_MODULE = "c29383a811f7c175e88cd9340fcceb5fe0845d134a409b9e434f7e6ab8953f23"
STOCK_GLCORE = "94b6c6191348c3095469162f643a58d6b9b85feb9c990b3097c6e8ebd3110afc"
VERSION = "620.70.06"
CASES = {"compute_bind_only_same", "compute_bind_only_alt2", "draw_only", "bind_many_then_one_draw",
         "bind_draw", "scope_draw_no_bind", "bind_every_1_draws", "bind_every_2_draws", "bind_every_4_draws",
         "bind_every_8_draws", "bind_every_16_draws", "bind_every_64_draws", "one_draw_indirect",
         "dispatch_only", "bind_compute_dispatch"}


def digest(path):
    with Path(path).open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def write_json(path, data):
    Path(path).write_text(json.dumps(data, indent=2) + "\n")


def output_directory(value):
    path = Path(value).resolve()
    if not path.is_relative_to(ROOT / "artifacts"):
        raise ValueError("raw output must be inside the ignored artifacts directory")
    path.mkdir(parents=True, exist_ok=False)
    return path


def run_command(command, env, log, timeout=180):
    with Path(log).open("w") as f:
        result = subprocess.run([str(x) for x in command], env=env, cwd=ROOT,
                                stdout=f, stderr=subprocess.STDOUT, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}); see {Path(log).name}")
    return Path(log).read_text()


def policy_responses(log):
    return set(re.findall(r"RM_POLICY command=20810107 status=00000000 response=([0-9a-f]{2} [0-9a-f]{2})", log))


def check_policy(log, device_id, smoke=False):
    responses = policy_responses(log)
    if not responses:
        raise ValueError("no successful RM policy response was observed")
    if not smoke and responses != ({"01 01"} if device_id == "1f0b" else {"00 00"}):
        raise ValueError("RM policy does not match the requested stock-driver state")
    return sorted(responses)


def check_identity(data, expected):
    identity = expected + "10de"
    if data.get("config_pci_identity") != identity or data.get("bar0_xve_identity") != identity:
        raise ValueError("physical config/BAR0 identity does not match the test state")
    if data.get("cached_pci_identity") != "1f0b10de" or data.get("boot0") != "166000a1":
        raise ValueError("unexpected cached identity or GPU architecture")


def smi_state(text, expected, smoke):
    root = ET.fromstring(text)
    gpus = root.findall("gpu")
    if len(gpus) != 1:
        raise ValueError("expected exactly one GPU in the targeted NVML output")
    gpu = gpus[0]
    if int(gpu.findtext("pci/pci_device_id", "0"), 16) != int(expected + "10de", 16):
        raise ValueError("NVML Device ID does not match the test state")
    if root.findtext("driver_version") != VERSION:
        raise ValueError("this protocol requires NVIDIA 620.70.06")
    if gpu.findtext("gsp_firmware_version") != "N/A":
        raise ValueError("GSP must be OFF")
    if not smoke and "Open Kernel" in Path("/proc/driver/nvidia/version").read_text():
        raise ValueError("the comparison requires proprietary CPU RM")
    return {"driver": VERSION, "gsp": "N/A", "pci": gpu.findtext("pci/pci_bus_id")}


def clpeak_index(listing, name, aliases):
    matches = []
    for match in re.finditer(r"Vulkan Device\s+(\d+):\s*([^\n]+)", listing):
        listed_name = re.sub(r"\s+\[[^\]]+\]$", "", match[2].strip())
        if listed_name == name:
            matches.append(int(match[1]))
    if len(matches) != aliases or not matches:
        raise ValueError("clpeak device-name matches must agree with the deduplicated Vulkan inventory")
    return matches[0]


def clpeak_metrics(path, name, backend, expected_driver):
    metrics = {}
    with Path(path).open() as f:
        rows = list(csv.DictReader(f))
    if not rows:
        raise ValueError("clpeak produced no CSV entries")
    for row in rows:
        if row["device"] != name or row["driver"] != expected_driver or row["backend"] != backend:
            raise ValueError("clpeak CSV names a different GPU, driver or backend")
        if row["status"] != "ok":
            continue
        value = float(row["value"])
        if not math.isfinite(value) or value <= 0:
            raise ValueError("clpeak returned a nonpositive or nonfinite score")
        key = "/".join((backend, row["test"], row["metric"], row["unit"]))
        if key in metrics:
            raise ValueError("duplicate clpeak metric")
        metrics[key] = value
    if not metrics:
        raise ValueError("clpeak produced no successful metrics")
    return metrics


def clpeak_driver_label(backend, data):
    if backend == "Vulkan":
        raw = data["driver_version_raw"]
        return f"{raw >> 22}.{(raw >> 12) & 1023}.{raw & 4095}"
    api = data["driver_api_version"]
    return f"{api // 1000}.{api % 1000 // 10}"


class CudaDiscoveryUnavailable(RuntimeError):
    pass


def clpeak_selection(backend, target, manifest, listing):
    if backend == "Vulkan":
        return (clpeak_index(listing, target["device_name"], target["aliases"]),
                target["device_name"], clpeak_driver_label(backend, target))
    cuda = manifest.get("cuda")
    if not cuda:
        raise CudaDiscoveryUnavailable("CUDA device discovery failed before clpeak benchmarking; see cuda-device.json")
    return cuda["cuda_index"], cuda["device_name"], clpeak_driver_label(backend, cuda)


def workloads(smoke):
    if smoke:
        return [{"name": "smoke", "width": 64, "height": 64, "draws": 4, "iters": 8}]
    return [
        {"name": "bind_1000_same", "case": "compute_bind_only_same", "width": 64, "height": 64, "draws": 1000, "iters": 0},
        {"name": "bind_1000_alt2", "case": "compute_bind_only_alt2", "width": 64, "height": 64, "draws": 1000, "iters": 0},
        {"name": "commands_100", "width": 256, "height": 256, "draws": 100, "iters": 2},
        {"name": "fragment_alu", "case": "draw_only", "width": 1920, "height": 1080, "draws": 4, "iters": 256},
        {"name": "compute_alu", "case": "dispatch_only", "width": 1920, "height": 1080, "draws": 4, "iters": 256},
    ]


def collect(args):
    if not args.smoke and not args.stock_loaded:
        raise ValueError("--stock-loaded must acknowledge a manual insmod of the prepared stock module")
    out = output_directory(args.out)
    env = os.environ.copy()
    removed = [k for k in env if k.startswith(("LD_", "VK_", "__GL", "__NV", "CUDA_"))]
    for key in removed:
        env.pop(key)
    env["VK_LOADER_LAYERS_DISABLE"] = "*"
    manifest = {"schema": 1, "mode": "smoke" if args.smoke else "stock", "device_id": args.device_id,
                "state": "running", "jobs": {}, "removed_environment_keys": removed,
                "workloads": workloads(args.smoke), "repeats": args.repeats,
                "warmup": args.warmup, "clpeak_repeats": args.clpeak_repeats,
                "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    telemetry = None
    telemetry_file = None
    try:
        source_paths = sorted((ROOT / "tools/devid/benchmarks").rglob("*")) + [Path(__file__).resolve(), ROOT / "tools/devid/observe_rm_policy.c"]
        manifest["sources"] = {str(p.relative_to(ROOT)): digest(p) for p in source_paths if p.is_file()}
        bench = Path(args.build).resolve() / "devid_vulkan"
        manifest["executables"] = {"vulkan": digest(bench)}
        if not args.smoke:
            module = ROOT / "artifacts/devid620/stock-kernel/nvidia.ko"
            if digest(module) != STOCK_MODULE:
                raise ValueError("prepared module differs from the recorded stock SHA256")
            manifest["stock_module_sha256"] = STOCK_MODULE
            manifest["stock_load_acknowledged"] = True
        manifest["loaded_srcversion"] = Path("/sys/module/nvidia/srcversion").read_text().strip()
        # srcversion can remain identical after binary patching; it is metadata,
        # not proof that loaded module memory is stock.
        if args.identity:
            identity = json.loads(Path(args.identity).read_text())
            check_identity(identity, args.device_id)
            write_json(out / "identity-before.json", identity)
        elif not args.smoke:
            raise ValueError("a fresh --identity read_identity.py snapshot is required")
        target = json.loads(run_command([bench, f"--device-id={args.device_id}", "--inspect"], env, out / "target.json"))
        if target["driver_id"] != 4 or target["driver_info"] != VERSION or not target["pci"]:
            raise ValueError("expected NVIDIA proprietary 620.70.06 with PCI bus properties")
        manifest["target"] = target
        pci = target["pci"]
        before = run_command(["nvidia-smi", "-i", pci, "-q", "-x"], env, out / "smi-before.xml")
        manifest["smi_before"] = smi_state(before, args.device_id, args.smoke)
        observe_env = env | {"LD_PRELOAD": str(Path(args.observer).resolve()), "LD_DEBUG": "libs"}
        observation = run_command(["vulkaninfo", "--summary"], observe_env, out / "policy-and-libraries.log")
        manifest["policy"] = check_policy(observation, args.device_id, args.smoke)
        loaded_glcore = sorted(set(re.findall(r"calling init:\s*(/[^\n]*libnvidia-glcore\.so[^\s]*)", observation)))
        if not loaded_glcore:
            raise ValueError("could not identify the glcore loaded by the policy probe")
        manifest["glcore"] = {p: digest(p) for p in loaded_glcore}
        if not args.smoke and set(manifest["glcore"].values()) != {STOCK_GLCORE}:
            raise ValueError("loaded userspace glcore differs from the recorded stock SHA256")
        run_command(["vulkaninfo"], env, out / "vulkaninfo.txt")
        telemetry_file = (out / "telemetry.csv").open("w")
        fields = "timestamp,pstate,temperature.gpu,power.draw,clocks.current.graphics,clocks.current.sm,clocks.current.memory,utilization.gpu,pcie.link.gen.current,pcie.link.width.current"
        telemetry = subprocess.Popen(["nvidia-smi", "-i", pci, f"--query-gpu={fields}", "--format=csv", "-lms", "250"],
                                     env=env, stdout=telemetry_file, stderr=subprocess.STDOUT)
        for workload in manifest["workloads"]:
            name = workload["name"]
            if not args.smoke:
                print(f"Preconditioning GPU before {name}", flush=True)
                deadline = time.monotonic() + 5
                precondition = 0
                while time.monotonic() < deadline:
                    run_command([bench, f"--device-id={args.device_id}", f"--shader-dir={Path(args.build).resolve()}",
                                 "--case=dispatch_only", "--width=1024", "--height=1024", "--draws=8", "--iters=256",
                                 "--warmup=0", "--repeats=10", f"--output={out / (name + '-precondition.csv')}"],
                                env, out / (name + "-precondition.log"))
                    precondition += 1
                manifest.setdefault("preconditioning", {})[name] = {"seconds": 5, "batches": precondition}
            command = [bench, f"--device-id={args.device_id}", f"--shader-dir={Path(args.build).resolve()}",
                       f"--repeats={args.repeats}", f"--warmup={args.warmup}", f"--output={out / (name + '.csv')}"]
            command += [f"--{k}={v}" for k, v in workload.items() if k != "name"]
            print(f"Running {name}", flush=True)
            run_command(command, env, out / (name + ".log"))
            manifest["jobs"][name] = {"status": "ok", "command": [str(x) for x in command]}
            write_json(out / "run.json", manifest)
        verifier = Path(args.build).resolve() / "verify_wmma"
        try:
            manifest["executables"]["wmma"] = digest(verifier)
            cuda = json.loads(run_command([verifier, f"--pci={pci}", "--inspect"], env, out / "cuda-device.json"))
            manifest["cuda"] = cuda
            output = run_command([verifier, f"--pci={pci}"], env, out / "wmma.log")
            if "WMMA_NUMERICAL_PASS:" not in output:
                raise ValueError("WMMA verifier produced no success marker")
            manifest["jobs"]["wmma"] = {"status": "ok"}
        except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
            manifest["jobs"]["wmma"] = {"status": "failed", "error": str(error)}
        if args.clpeak:
            clpeak = Path(args.clpeak).resolve()
            manifest["executables"]["clpeak"] = digest(clpeak)
            run_command([clpeak, "--version"], env, out / "clpeak-version.txt")
            for backend, index_flag in (("Vulkan", "--vk-device"), ("CUDA", "--cuda-device")):
                name = "clpeak-" + backend.lower()
                try:
                    listing = run_command([clpeak, "--" + backend.lower(), "--list-devices"], env, out / (name + "-devices.txt"))
                    index, expected_name, expected_driver = clpeak_selection(backend, target, manifest, listing)
                    metrics = {}
                    for rep in range(args.clpeak_repeats):
                        print(f"Running {name} repeat {rep + 1}", flush=True)
                        csv_path = out / f"{name}-{rep}.csv"
                        command = [clpeak, "--" + backend.lower(), index_flag, str(index),
                                   "--single-precision-compute", "--half-precision-compute", "--integer-compute",
                                   "--global-memory-bandwidth", "--local-memory-bandwidth", "--transfer-bandwidth",
                                   "--kernel-launch-latency", "--max-time", "150", "--warmup", str(args.warmup),
                                   "--csv-file", csv_path]
                        run_command(command, env, out / f"{name}-{rep}.log")
                        values = clpeak_metrics(csv_path, expected_name, backend, expected_driver)
                        if rep and values.keys() != metrics.keys():
                            raise ValueError("clpeak metric coverage changed between repeats")
                        for key, value in values.items():
                            metrics.setdefault(key, []).append(value)
                    manifest["jobs"][name] = {"status": "ok", "metrics": metrics, "device_index": index,
                                             "csv_driver_label": expected_driver}
                except CudaDiscoveryUnavailable as error:
                    manifest["jobs"][name] = {"status": "skipped", "reason": str(error),
                                             "backend_inventory_log": name + "-devices.txt"}
                except (OSError, KeyError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
                    manifest["jobs"][name] = {"status": "failed", "error": str(error)}
        else:
            manifest["jobs"]["clpeak"] = {"status": "skipped", "reason": "no executable supplied"}
        after = run_command(["nvidia-smi", "-i", pci, "-q", "-x"], env, out / "smi-after.xml")
        manifest["smi_after"] = smi_state(after, args.device_id, args.smoke)
        after_target = json.loads(run_command([bench, f"--device-id={args.device_id}", "--inspect"], env, out / "target-after.json"))
        if after_target != target:
            raise ValueError("Vulkan identity changed during collection")
        manifest["state"] = "complete"
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        manifest["state"] = "failed"
        manifest["error"] = str(error)
        raise
    finally:
        if telemetry:
            telemetry.terminate()
            try:
                telemetry.wait(timeout=5)
            except subprocess.TimeoutExpired:
                telemetry.kill()
                telemetry.wait()
            manifest["telemetry_available"] = (out / "telemetry.csv").stat().st_size > 0 and "NVIDIA-SMI has failed" not in (out / "telemetry.csv").read_text()
        if telemetry_file:
            telemetry_file.close()
        write_json(out / "run.json", manifest)
    print(f"Saved {out.relative_to(ROOT)}; optional category status:", flush=True)
    for name, job in manifest["jobs"].items():
        print(f"  {name}: {job['status']}")


def summary(values):
    if not values or any(not math.isfinite(x) or x < 0 for x in values):
        raise ValueError("invalid timing/score samples")
    median = statistics.median(values)
    return {"n": len(values), "median": median, "min": min(values), "max": max(values),
            "spread_pct": 100 * (max(values) - min(values)) / median if median else None}


def metrics_for(path, manifest):
    metrics = {}
    for workload in manifest["workloads"]:
        name = workload["name"]
        cases = {}
        with (path / (name + ".csv")).open() as f:
            for row in csv.DictReader(f):
                case = row["case"]
                repeat = int(row["repeat"])
                if repeat in cases.setdefault(case, set()):
                    raise ValueError("duplicate repeat in Vulkan CSV")
                cases[case].add(repeat)
                if not row["case"].startswith("compute_bind_only") and int(row["checked_pixels"]) != 9:
                    raise ValueError("missing graphics/compute correctness check")
                key = f"{name}/{row['case']}/gpu_ms"
                metrics.setdefault(key, []).append(float(row["gpu_ms"]))
        expected = {workload["case"]} if "case" in workload else CASES
        if cases.keys() != expected or any(reps != set(range(manifest["repeats"])) for reps in cases.values()):
            raise ValueError("incomplete Vulkan case/repeat coverage")
    for name, job in manifest["jobs"].items():
        if name.startswith("clpeak-") and job["status"] == "ok":
            metrics.update(job["metrics"])
    return metrics


def compare(args):
    paths = [Path(p).resolve() for p in args.runs]
    runs = [json.loads((p / "run.json").read_text()) for p in paths]
    if [r["device_id"] for r in runs] != ["1f0b", "1f4b", "1f0b"]:
        raise ValueError("comparison order must be A1(1f0b), B(1f4b), A2(1f0b)")
    for r in runs:
        if r["mode"] != "stock" or r["state"] != "complete":
            raise ValueError("only completed stock runs can be compared")
    for key in ("sources", "executables", "workloads", "repeats", "warmup", "clpeak_repeats", "stock_module_sha256"):
        if any(r.get(key) != runs[0].get(key) for r in runs[1:]):
            raise ValueError(f"runs differ in {key}")
    values = [metrics_for(p, r) for p, r in zip(paths, runs)]
    common = set.intersection(*(set(v) for v in values))
    omitted = sorted(set.union(*(set(v) for v in values)) - common)
    rows = []
    for key in sorted(common):
        stats = [summary(v[key]) for v in values]
        if any(s["n"] < 3 for s in stats):
            raise ValueError("at least three measured repeats per metric are required")
        a1, b, a2 = [s["median"] for s in stats]
        lower_is_better = key.endswith(("/gpu_ms", "/us"))
        rows.append({"metric": key, "A1": stats[0], "B": stats[1], "A2": stats[2],
                     "B_speedup_vs_A1": (a1 / b if b else None) if lower_is_better else (b / a1 if a1 else None),
                     "B_speedup_vs_A2": (a2 / b if b else None) if lower_is_better else (b / a2 if a2 else None),
                     "A2_change_pct": 100 * (a2 / a1 - 1) if a1 else None,
                     "B_near_timestamp_floor": "compute_bind_only" in key and b < 0.01})
    if not rows:
        raise ValueError("no comparable metrics")
    out = output_directory(args.out)
    write_json(out / "comparison.json", {"rows": rows, "omitted_metrics": omitted,
               "category_status": [r["jobs"] for r in runs]})
    with (out / "comparison.csv").open("w") as f:
        writer = csv.writer(f)
        writer.writerow(["metric", "A1_median", "B_median", "A2_median", "speedup_vs_A1", "speedup_vs_A2", "A2_change_pct"])
        for row in rows:
            writer.writerow([row["metric"], row["A1"]["median"], row["B"]["median"], row["A2"]["median"],
                             row["B_speedup_vs_A1"], row["B_speedup_vs_A2"], row["A2_change_pct"]])
    print(f"Compared {len(rows)} metrics; omitted {len(omitted)}. Saved {out.relative_to(ROOT)}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    run = commands.add_parser("run")
    run.add_argument("--device-id", choices=["1f0b", "1f4b"], required=True)
    run.add_argument("--out", required=True)
    run.add_argument("--build", default="artifacts/devid620/bench-build")
    run.add_argument("--observer", default="artifacts/devid620/observe_rm_policy.so")
    run.add_argument("--identity")
    run.add_argument("--clpeak")
    run.add_argument("--stock-loaded", action="store_true")
    run.add_argument("--smoke", action="store_true")
    run.add_argument("--repeats", type=int, default=7)
    run.add_argument("--warmup", type=int, default=2)
    run.add_argument("--clpeak-repeats", type=int, default=5)
    comparison = commands.add_parser("compare")
    comparison.add_argument("runs", nargs=3)
    comparison.add_argument("--out", required=True)
    args = parser.parse_args()
    if args.command == "run":
        if not 1 <= args.repeats <= 100 or not 0 <= args.warmup <= 20 or not 1 <= args.clpeak_repeats <= 20:
            parser.error("repeat/warmup counts are out of bounds")
        collect(args)
    else:
        compare(args)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(f"benchmark collection failed: {error}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Boot real EFI images on emulated PCI hardware, with private OVMF variables.

Uses only temporary test FAT volumes. No host block device, GPU passthrough,
real Windows loader, ESP installation or system firmware variable is used.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tempfile
import time
from fat16_fixture import Fat16, CLUSTER_BYTES, SECTORS, DATA_SECTOR
from fat32_fixture import Fat32
from trace_log import transform, managed_payload, fnv

WORKSPACE = Path(__file__).resolve().parents[1]
GEN2_CASES = [f"gen2-{mode}-{control}" for mode in ("prepare", "targets", "retrain")
              for control in ("no-gpu", "optout")]


def firmware(explicit, filename):
    if explicit:
        path = Path(explicit)
        if not path.is_file():
            raise ValueError(f"firmware file not found: {path}")
        return path
    for root in ("/usr/share/edk2-ovmf/x64", "/usr/share/OVMF", "/usr/share/OVMF/x64",
                 "/usr/share/edk2/x64"):
        for name in (filename, filename.replace(".4m", "_4M")):
            path = Path(root) / name
            if path.is_file():
                return path
    raise ValueError(f"cannot find {filename}; pass --ovmf-code/--ovmf-vars")


def check_efi(path):
    data = path.read_bytes()
    if data[:2] != b"MZ":
        raise ValueError(f"missing PE header: {path}")
    pe = struct.unpack_from("<I", data, 0x3c)[0]
    if data[pe:pe + 4] != b"PE\0\0" or struct.unpack_from("<H", data, pe + 4)[0] != 0x8664:
        raise ValueError(f"not an x86-64 PE image: {path}")
    optional = pe + 24
    if struct.unpack_from("<H", data, optional)[0] != 0x20b:
        raise ValueError(f"not PE32+: {path}")
    if struct.unpack_from("<H", data, optional + 68)[0] != 10:
        raise ValueError(f"not an EFI application: {path}")
    if struct.unpack_from("<I", data, optional + 16)[0] == 0:
        raise ValueError(f"missing entry point: {path}")
    if struct.unpack_from("<II", data, optional + 112 + 5 * 8) == (0, 0):
        raise ValueError(f"missing base relocations: {path}")
    return hashlib.sha256(data).hexdigest()


def copy(source, root, relative):
    destination = root / relative
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)


def run_case(case, artifacts, binaries, qemu, code, variables, timeout):
    root = artifacts / case
    root.mkdir()
    esp = root / "esp"
    esp.mkdir()
    app, windows, parent = binaries[:3]
    volumes = [esp]
    rebar_case = case.startswith("rebar-")
    gen2_case = case.startswith("gen2-")
    unlock = case in ("cpu-full-no-gpu", "cpu-zero-no-gpu") or rebar_case or gen2_case
    adapter_case = case in ("logging", "failed-parent", "logcheck", "failed-start", "retention", "volume-full")
    raw_case = case.startswith("raw-")
    entry_case = case.startswith("entrycheck")
    disk_image = None
    extra_disks = []
    if gen2_case:
        mode = case.split("-")[1]
        image = next(path for path in binaries if path.name == f"cmp40-gen2-{mode}.efi")
        required = ["CMPUNLOCKER: BEGIN control=full",
                    f"CMPUNLOCKER-GEN2: MODE {'Off' if case.endswith('optout') else {'prepare': 'Prepare', 'targets': 'Targets', 'retrain': 'Retrain'}[mode]} GPU_WRITES=0",
                    "GPU_NOT_FOUND NOT_FOUND GPU_WRITES=0"]
        forbidden = ["CMPUNLOCKER-GEN2: BEGIN", "CMPUNLOCKER-GEN2: FINAL_BEGIN", "CMPUNLOCKER-REBAR: BEGIN", "TRANSACTION_BEGIN", "NATIVE_READY", "WINDOWS_PREPARED"]
        if case.endswith("optout"):
            fixture = next(path for path in binaries if path.name == "cmp40-fixture-rebar-policy.efi")
            copy(fixture, esp, "EFI/BOOT/BOOTX64.EFI")
            copy(image, esp, "EFI/CMP40/FULL.EFI")
            (esp / "MODE.TXT").write_bytes(b"no-gen2")
            required.insert(0, "START_REBAR_POLICY_CHILD")
            required.append("REBAR_POLICY_CHILD_RETURNED NOT_FOUND GPU_WRITES=0")
            forbidden += ["REBAR_POLICY_FAILED"]
        else:
            copy(image, esp, "EFI/BOOT/BOOTX64.EFI")
    elif rebar_case:
        fixture = next(path for path in binaries if path.name == "cmp40-fixture-rebar-policy.efi")
        copy(fixture, esp, "EFI/BOOT/BOOTX64.EFI")
        copy(binaries[3], esp, "EFI/CMP40/FULL.EFI")
        (esp / "MODE.TXT").write_bytes(b"optout" if case == "rebar-optout" else b"literal" if case == "rebar-literal" else b"trace" if case == "rebar-trace" else b"")
        policies = {"rebar-enabled": b"CMP40HX_REBAR_V1=1\n",
                    "rebar-disabled": b"CMP40HX_REBAR_V1=0\n",
                    "rebar-invalid": b"CMP40HX_REBAR_V1=1\nextra",
                    "rebar-optout": b"CMP40HX_REBAR_V1=1\n"}
        if case in policies:
            policy = esp / "EFI/40HX/REBAR.CFG"
            policy.parent.mkdir(parents=True)
            policy.write_bytes(policies[case])
        if case == "rebar-other-volume":
            other = root / "other-esp"
            other.mkdir()
            policy = other / "EFI/40HX/REBAR.CFG"
            policy.parent.mkdir(parents=True)
            policy.write_bytes(b"CMP40HX_REBAR_V1=1\n")
            volumes.append(other)
        expected = "true" if case in ("rebar-enabled", "rebar-literal") else "false"
        required = ["CMPUNLOCKER-FIXTURE: REBAR_MEMORY_MAP_OK GPU_WRITES=0",
                    "START_REBAR_POLICY_CHILD", "CMPUNLOCKER: BEGIN",
                    f"requested={expected}", "GPU_NOT_FOUND NOT_FOUND GPU_WRITES=0",
                    "REBAR_POLICY_CHILD_RETURNED NOT_FOUND GPU_WRITES=0"]
        required.insert(3, f"LOG_MODE trace={'true' if case == 'rebar-trace' else 'false'} GPU_WRITES=0")
        forbidden = ["REBAR_POLICY_FAILED", "CMPUNLOCKER-REBAR: BEGIN", "TRANSACTION_BEGIN", "NATIVE_READY", "WINDOWS_PREPARED"]
    elif entry_case:
        checker = next(path for path in binaries if path.name == "cmp40-entrycheck.efi")
        copy(checker, esp, "EFI/BOOT/BOOTX64.EFI")
        copy(binaries[3], esp, "EFI/CMP40/FULLCHK.EFI")
        copy(binaries[4], esp, "EFI/CMP40/ZEROCHK.EFI")
        if case == "entrycheck-invalid":
            (esp / "EFI/CMP40/ZEROCHK.EFI").write_bytes(b"MZ invalid candidate image")
        copy(windows, esp, "EFI/Microsoft/Boot/bootmgfw.efi")
        fat = Fat16()
        for file in esp.rglob("*"):
            if file.is_file():
                fat.add(file.relative_to(esp).as_posix(), file.read_bytes())
        disk_image = root / "esp.img"
        disk_image.write_bytes(fat.data)
        zero_marker = "LOAD_REFUSED" if case == "entrycheck-invalid" else "LOAD_OK"
        required = ["CMPUNLOCKER-ENTRYCHECK: ENTERED GPU_WRITES=0 CHILD_STARTS=0",
                    "CMPUNLOCKER-ENTRYCHECK: BEGIN", "POLICY_OK parent=false",
                    "PCI_RESULT NOT_FOUND GPU_WRITES=0",
                    f"{zero_marker} ZERO route=Firmware", f"{zero_marker} ZERO route=RawBuffer",
                    "LOAD_OK FULL route=Firmware", "LOAD_OK FULL route=RawBuffer",
                    "CMPUNLOCKER-ENTRYCHECK: WINDOWS_PREPARED source=RawBuffer CHILD_STARTS=0",
                    "CMPUNLOCKER-ENTRYCHECK: READY GPU_WRITES=0 CHILD_STARTS=0",
                    "CMPUNLOCKER-ENTRYCHECK: FINISHED SUCCESS GPU_WRITES=0 CHILD_STARTS=0"]
        forbidden = ["CMPUNLOCKER: BEGIN", "TRANSACTION_BEGIN", "NATIVE_READY",
                     "WINDOWS_CHAIN_REACHED", "START_WINDOWS", "LOG_OPEN_FAILED",
                     "CMPUNLOCKER-ENTRYCHECK: FAILED"]
    elif raw_case:
        raw_parent = next(path for path in binaries if path.name == "cmp40-fixture-raw.efi")
        copy(raw_parent, esp, "EFI/BOOT/BOOTX64.EFI")
        copy(app, esp, "EFI/CMP40/READONLY.EFI")
        mode = b"parent" if case == "raw-parent" else b""
        (esp / "MODE.TXT").write_bytes(mode)
        if case not in ("raw-ambiguous", "raw-parent") and not case.startswith("raw-gpt-"):
            copy(windows, esp, "EFI/Microsoft/Boot/bootmgfw.efi")
        fat = Fat16()
        for file in esp.rglob("*"):
            if file.is_file() and file.relative_to(esp).as_posix() != "EFI/Microsoft/Boot/bootmgfw.efi":
                fat.add(file.relative_to(esp).as_posix(), file.read_bytes())
        if (esp / "EFI/Microsoft/Boot/bootmgfw.efi").exists():
            # Fragmentation/long names are checked by host tests; a self-cycle
            # here must stop before LoadImage even when firmware SFS is broken.
            before = fat.next_cluster
            fat.add("EFI/Microsoft/Boot/bootmgfw.efi", windows.read_bytes())
            if case == "raw-corrupt-chain":
                boot_directory = fat.directories[("EFI", "MICROSOFT", "BOOT")]
                offset = fat.cluster_offset(boot_directory) + 2 * 32 + 26
                first = struct.unpack_from("<H", fat.data, offset)[0]
                assert first >= before and len(windows.read_bytes()) > CLUSTER_BYTES
                fat.fat(first, first)
        disk_image = root / "esp.img"
        disk_image.write_bytes(fat.data)
        required = ["SFS_BLOCKED START_RAW_CHILD"]
        forbidden = ["RAW_PARENT_FAILED", "SFS_CALLS=1", "WINDOWS_VOLUME_INVALID", "TRANSACTION_BEGIN"]
        if case.startswith("raw-gpt-"):
            block = 4096 if case == "raw-gpt-4k" else 512
            disk = Fat32(block)
            disk.add("EFI/Microsoft/Boot/bootmgfw.efi", windows.read_bytes())
            image = root / "windows-gpt.img"
            disk.write_gpt(image)
            if case == "raw-gpt-bad-crc":
                with image.open("r+b") as output:
                    output.seek(block + 56)
                    output.write(b"\x77")
                required += ["WINDOWS_PREPARE_FAILED VOLUME_CORRUPTED GPU_WRITES=0", "RAW_CHILD_RETURNED VOLUME_CORRUPTED SFS_CALLS=0"]
                forbidden += ["START_WINDOWS", "WINDOWS_CHAIN_REACHED"]
            else:
                required += [f"FAT=Fat32 block_bytes={block} SFS_CALLS=0", "WINDOWS_FROM_BUFFER", "WINDOWS_PREPARED unique-volume", "START_WINDOWS GPU_WRITES=0", "WINDOWS_VOLUME_OK", "WINDOWS_CHAIN_REACHED", "RAW_CHILD_RETURNED SUCCESS SFS_CALLS=0"]
            extra_disks.append((image, block))
        elif case == "raw-corrupt-chain":
            required += ["WINDOWS_PREPARE_FAILED VOLUME_CORRUPTED GPU_WRITES=0", "RAW_CHILD_RETURNED VOLUME_CORRUPTED SFS_CALLS=0"]
            forbidden += ["START_WINDOWS", "WINDOWS_CHAIN_REACHED"]
        elif case == "raw-ambiguous":
            for number in (1, 2):
                volume = root / f"windows-esp-{number}"
                volume.mkdir()
                copy(windows, volume, "EFI/Microsoft/Boot/bootmgfw.efi")
                volumes.append(volume)
            required += ["WINDOWS_AMBIGUOUS", "RAW_CHILD_RETURNED NO_MAPPING SFS_CALLS=0"]
            forbidden += ["START_WINDOWS", "WINDOWS_CHAIN_REACHED"]
        elif case == "raw-parent":
            required += ["RETURN_PARENT GPU_WRITES=0", "RAW_CHILD_RETURNED SUCCESS SFS_CALLS=0"]
            forbidden += ["START_WINDOWS", "WINDOWS_CHAIN_REACHED"]
        else:
            required += ["WINDOWS_FROM_BUFFER", "WINDOWS_PREPARED own-volume", "START_WINDOWS GPU_WRITES=0", "WINDOWS_VOLUME_OK", "WINDOWS_CHAIN_REACHED", "RAW_CHILD_RETURNED SUCCESS SFS_CALLS=0"]
    elif adapter_case:
        if case == "logcheck":
            copy(binaries[7], esp, "EFI/BOOT/BOOTX64.EFI")
            required = ["CMPUNLOCKER-LOGCHECK: BEGIN GPU_WRITES=0", "LOG_VERIFIED",
                        "CMPUNLOCKER-LOGCHECK: LOG_CHECK_READY GPU_WRITES=0"]
        elif case in ("logging", "retention", "volume-full"):
            if case == "retention":
                (esp / "ROTATE.TXT").write_bytes(b"1")
            copy(binaries[5], esp, "EFI/BOOT/BOOTX64.EFI")
            required = ["LOG_VERIFIED", "CMPUNLOCKER-FIXTURE: LOG_TEST_READY GPU_WRITES=0"]
        elif case == "failed-start":
            copy(binaries[6], esp, "EFI/BOOT/BOOTX64.EFI")
            child = next(path for path in binaries if path.name == "cmp40-fixture-return-error.efi")
            copy(child, esp, "EFI/CMP40/FAILRET.EFI")
            (esp / "STARTERR.TXT").write_bytes(b"1")
            required = ["START_ERROR_CHILD", "RETURN_DEVICE_ERROR", "START_IMAGE_FAILED DEVICE_ERROR", "FAILURE_PARKED DEVICE_ERROR GPU_ATTEMPTED=1"]
        else:
            copy(binaries[6], esp, "EFI/BOOT/BOOTX64.EFI")
            copy(binaries[5], esp, "EFI/CMP40/FAILED.EFI")
            required = ["START_FAILURE_CHILD", "LOG_TEST_READY GPU_WRITES=0",
                        "SIMULATED_POST_MUTATION_FAILURE", "FAILURE_PARKED DEVICE_ERROR GPU_ATTEMPTED=1"]
        forbidden = ["LOG_TEST_FAILED", "CMPUNLOCKER-LOGCHECK: FAILED", "PARENT_RESUMED_AFTER_FAILURE", "WINDOWS_CHAIN_REACHED", "FAILED_PARENT_ERROR"]
        fat = Fat16()
        for file in esp.rglob("*"):
            if file.is_file():
                fat.add(file.relative_to(esp).as_posix(), file.read_bytes())
        if case == "retention":
            fat.add("RF000999.LOG", b"legacy preserved")
            fat.add("EFI/40HX/LOGS/L0001000.LOG", b"foreign preserved")
            fat.add("EFI/40HX/LOGS/KEEP.TXT", b"unrelated preserved")
            # This valid old summary must count towards the same rotation quota.
            body = b"CMP40_LOG_V3 generation=0000999 control=full\nold summary\n"
            footer = f"CMP40_END_V3 kind=S bytes={len(body):020} fnv={fnv(body):016x}\n"
            fat.add("EFI/40HX/LOGS/L0000999.LOG", body + footer.encode())
            required.insert(1, "RETENTION_READY GPU_WRITES=0")
        if case == "volume-full":
            fat.add("RF000999.LOG", b"legacy preserved")
            fat.add("EFI/40HX/LOGS/KEEP.TXT", b"unrelated preserved")
            remaining = (SECTORS - DATA_SECTOR) // 4 - (fat.next_cluster - 2)
            fat.add("FILLER.BIN", b"x" * (remaining * CLUSTER_BYTES))
            required = ["LOG_TEST_FAILED VOLUME_FULL"]
            forbidden.remove("LOG_TEST_FAILED")
            forbidden += ["LOG_TEST_READY", "FAILURE_PARKED"]
        disk_image = root / "esp.img"
        disk_image.write_bytes(fat.data)
    elif unlock:
        app = binaries[3 if case == "cpu-full-no-gpu" else 4]
        copy(app, esp, "EFI/BOOT/BOOTX64.EFI")
        required = ["CMPUNLOCKER: BEGIN", "GPU_NOT_FOUND NOT_FOUND GPU_WRITES=0"]
        if any(path.name == "cmp40-gen2-prepare.efi" for path in binaries):
            required.insert(1, "CMPUNLOCKER-GEN2: MODE Off GPU_WRITES=0")
        forbidden = ["HANDOFF_PREPARED", "TRANSACTION_BEGIN", "NATIVE_READY", "WINDOWS_PREPARED"]
        forbidden += ["CMPUNLOCKER-GEN2: BEGIN", "CMPUNLOCKER-GEN2: FINAL_BEGIN"]
    elif case == "parent":
        copy(parent, esp, "EFI/BOOT/BOOTX64.EFI")
        copy(app, esp, "EFI/CMP40/READONLY.EFI")
        required = ["CMPUNLOCKER-FIXTURE: START_READONLY_AS_CHILD", "RETURN_PARENT GPU_WRITES=0",
                    "CMPUNLOCKER-FIXTURE: PARENT_RESUMED"]
        forbidden = ["START_WINDOWS", "WINDOWS_CHAIN_REACHED", "PARENT_FAILED"]
    else:
        copy(app, esp, "EFI/BOOT/BOOTX64.EFI")
        forbidden = ["PARENT_FAILED"]
        if case == "windows":
            copy(windows, esp, "EFI/Microsoft/Boot/bootmgfw.efi")
            required = ["WINDOWS_PREPARED own-volume", "START_WINDOWS GPU_WRITES=0",
                        "CMPUNLOCKER-FIXTURE: WINDOWS_CHAIN_REACHED", "WINDOWS_RETURNED SUCCESS"]
        elif case == "unique-windows":
            volume = root / "windows-esp"
            volume.mkdir()
            copy(windows, volume, "EFI/Microsoft/Boot/bootmgfw.efi")
            volumes.append(volume)
            required = ["WINDOWS_PREPARED unique-volume", "START_WINDOWS GPU_WRITES=0",
                        "CMPUNLOCKER-FIXTURE: WINDOWS_CHAIN_REACHED", "WINDOWS_RETURNED SUCCESS"]
        elif case == "no-windows":
            required = ["WINDOWS_PREPARE_FAILED NOT_FOUND GPU_WRITES=0"]
            forbidden += ["START_WINDOWS", "WINDOWS_CHAIN_REACHED"]
        elif case == "invalid-windows":
            path = esp / "EFI/Microsoft/Boot/bootmgfw.efi"
            path.parent.mkdir(parents=True)
            path.write_bytes(b"MZ invalid image for the negative boot test")
            required = ["WINDOWS_PREPARE_FAILED"]
            forbidden += ["START_WINDOWS", "WINDOWS_CHAIN_REACHED"]
        elif case == "ambiguous-windows":
            for number in (1, 2):
                volume = root / f"windows-esp-{number}"
                volume.mkdir()
                copy(windows, volume, "EFI/Microsoft/Boot/bootmgfw.efi")
                volumes.append(volume)
            required = ["WINDOWS_AMBIGUOUS", "WINDOWS_PREPARE_FAILED NO_MAPPING GPU_WRITES=0"]
            forbidden += ["START_WINDOWS", "WINDOWS_CHAIN_REACHED"]
        elif case == "self-windows":
            copy(app, esp, "EFI/Microsoft/Boot/bootmgfw.efi")
            required = ["START_WINDOWS GPU_WRITES=0", "SELF_WINDOWS_PATH_SKIPPED",
                        "WINDOWS_PREPARE_FAILED NOT_FOUND GPU_WRITES=0", "WINDOWS_RETURNED NOT_FOUND"]
            forbidden += ["WINDOWS_CHAIN_REACHED"]
        else:
            raise ValueError(f"unknown case: {case}")
    private_variables = root / "OVMF_VARS.fd"
    shutil.copyfile(variables, private_variables)
    serial, stderr = root / "serial.log", root / "qemu.log"
    command = [qemu, "-machine", "q35,accel=tcg", "-m", "256M", "-net", "none",
               "-display", "none", "-monitor", "none", "-serial", f"file:{serial}",
               "-drive", f"if=pflash,format=raw,readonly=on,file={code}",
               "-drive", f"if=pflash,format=raw,file={private_variables}"]
    if disk_image:
        command += ["-drive", f"if=virtio,format=raw,file={disk_image},id=disk0"]
        for index, volume in enumerate(volumes[1:], 1):
            command += ["-drive", f"if=virtio,format=raw,readonly=on,file=fat:{volume},id=disk{index}"]
        for index, (image, block) in enumerate(extra_disks, len(volumes)):
            # OVMF may repair a corrupt primary GPT from its backup on writable
            # media. Keep this target read-only so corruption reaches the app.
            command += ["-drive", f"if=none,format=raw,readonly=on,file={image},id=disk{index}", "-device", f"virtio-blk-pci,drive=disk{index},logical_block_size={block},physical_block_size={block}"]
    else:
        for index, volume in enumerate(volumes):
            command += ["-drive", f"if=virtio,format=raw,readonly=on,file=fat:{volume},id=disk{index}"]
    started = time.monotonic()
    passed = False
    ready_since = None
    with stderr.open("wb") as log:
        process = subprocess.Popen(command, stdout=log, stderr=log)
        try:
            while time.monotonic() - started < timeout:
                text = serial.read_text(errors="replace") if serial.exists() else ""
                if any(marker in text for marker in forbidden):
                    raise RuntimeError(f"{case}: forbidden boot action; inspect {serial}")
                pci = re.search(r"PCI_HANDLES=(\d+) TARGETS=0 READ_ERRORS=0", text)
                inventory_ok = unlock or adapter_case or entry_case or (pci and int(pci[1]) > 0
                                          and "INVENTORY_DONE GPU_WRITES=0" in text)
                if (inventory_ok
                        and all(marker in text for marker in required)):
                    positions = [text.index(marker) for marker in required]
                    if positions != sorted(positions):
                        raise RuntimeError(f"{case}: incorrect handoff order; inspect {serial}")
                    if case == "self-windows" and text.count("START_WINDOWS GPU_WRITES=0") != 1:
                        raise RuntimeError(f"{case}: recursive Windows start; inspect {serial}")
                    if case == "invalid-windows" and not re.search(
                            r"WINDOWS_PREPARE_FAILED (?:LOAD_ERROR|UNSUPPORTED) GPU_WRITES=0", text):
                        raise RuntimeError(f"{case}: unexpected rejection status; inspect {serial}")
                    if case in ("failed-parent", "failed-start"):
                        if ready_since is None:
                            ready_since = time.monotonic()
                        if time.monotonic() - ready_since < 1:
                            time.sleep(0.05)
                            continue
                    passed = True
                    break
                if process.poll() is not None:
                    raise RuntimeError(f"{case}: QEMU exited {process.returncode}; inspect {stderr}")
                time.sleep(0.05)
            if not passed:
                raise RuntimeError(f"{case}: timed out after {timeout}s; inspect {serial} and {stderr}")
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
    if entry_case:
        actual = Fat16(disk_image.read_bytes()).read_file("EFI/40HX/LOGS/L0000001.LOG")
        text = managed_payload(actual.decode())
        for marker in required[1:-1]:
            if marker not in text:
                raise RuntimeError(f"{case}: persisted early log is missing {marker}")
        if any(marker in text for marker in forbidden):
            raise RuntimeError(f"{case}: forbidden action recorded in early log")
        (root / "RE000001.LOG").write_bytes(actual)
    elif adapter_case:
        fat = Fat16(disk_image.read_bytes())
        if case == "volume-full":
            assert fat.read_root_file("RF000999.LOG") == b"legacy preserved"
            assert fat.read_file("EFI/40HX/LOGS/KEEP.TXT") == b"unrelated preserved"
            print("Full FAT refused logging before any GPU access; unrelated files preserved", flush=True)
        if case in ("failed-start", "volume-full"):
            return {"case": case, "passed": passed, "seconds": round(time.monotonic() - started, 2), "serial_log": str(serial), "qemu_command": command}
        if case == "retention":
            assert fat.read_root_file("RF000999.LOG") == b"legacy preserved"
            assert fat.read_file("EFI/40HX/LOGS/L0001000.LOG") == b"foreign preserved"
            assert fat.read_file("EFI/40HX/LOGS/KEEP.TXT") == b"unrelated preserved"
            summaries, traces, total, interrupted, latest = 0, 0, 0, False, False
            for entry in fat.directory_entries("EFI/40HX/LOGS"):
                stem = entry[:8].decode().strip()
                if not re.fullmatch(r"L[0-9]{7}", stem) or stem == "L0001000": continue
                assert stem != "L0000999", "old-format summary was not rotated"
                data = fat.read_file(f"EFI/40HX/LOGS/{stem}.LOG")
                assert data.startswith(b"CMPUNLOCKER_LOG_V3 ")
                total += len(data)
                if b"RETENTION_INTERRUPTED" in data:
                    assert b"CMPUNLOCKER_END_V3" not in data
                    interrupted = True
                    traces += 1
                else:
                    payload = managed_payload(data.decode())
                    latest |= "RETENTION_AFTER_INTERRUPT" in payload
                    if b"CMPUNLOCKER_END_V3 kind=T" in data: traces += 1
                    else: summaries += 1
            assert summaries == 32 and traces <= 4 and total <= 8 * 1024 * 1024
            assert interrupted and latest
            print(f"Retained summaries={summaries} traces={traces} bytes={total}; old/foreign files preserved", flush=True)
            return {"case": case, "passed": passed, "seconds": round(time.monotonic() - started, 2), "serial_log": str(serial), "qemu_command": command}

        first = (b"LOG_FIXTURE_FIRST\n" + b"".join(
            f"line={index:03} abcdefghijklmnopqrstuvwxyz\n".encode() for index in range(160))
            + b"".join(f"META {index:02x} {bytes(range(index,index+32)).hex()}\n".encode() for index in range(0,256,32))
            + b"LOG_FIXTURE_TAIL\n")
        expected_logs = (("RL000001.LOG", b"CMPUNLOCKER-LOGCHECK: FILE_WRITE_TEST GPU_WRITES=0\nCMPUNLOCKER-LOGCHECK: READY GPU_WRITES=0\n"),) if case == "logcheck" else (
            ("RZ000001.LOG", first), ("RZ000002.LOG", b"LOG_FIXTURE_SECOND\n"))
        for name, expected in expected_logs:
            actual = managed_payload(fat.read_file("EFI/40HX/LOGS/L0000001.LOG" if case == "logcheck" else "EFI/40HX/LOGS/L0000001.LOG" if name == "RZ000001.LOG" else "EFI/40HX/LOGS/L0000002.LOG").decode()).encode()
            if actual != expected:
                raise RuntimeError(f"{case}: persisted {name} differs from written content")
            (root / name).write_bytes(actual)
        if case != "logcheck":
            for index, (name, requested, failed, diagnostic) in enumerate([
                    ("SUMMARY", False, False, False), ("EXPLICIT", True, False, False),
                    ("LATE_FAILURE", False, True, False), ("ROLLBACK", False, False, True)], 3):
                filename = f"RZ{index:06}.LOG"
                actual = managed_payload(fat.read_file(f"EFI/40HX/LOGS/L{index:07}.LOG").decode())
                full = requested or failed or diagnostic
                mode = "trace" if full else "summary"
                flag = lambda value: str(value).lower()
                expected = (f"LOG_FIXTURE_MODE {name}\nLOG_DETAIL mode={mode} "
                            f"requested={flag(requested)} failed={flag(failed)} diagnostic={flag(diagnostic)}\n")
                if full:
                    expected += ("TRACE_BEGIN version=2 phase=UNLOCK records=20002 encoded_records=5 "
                                 "operations=20003 omitted_data_ports=2 dropped=0\n"
                                 "R 001100f4 00000000 count=1\nS 00000000 00000064 count=1\n"
                                 "REPEAT_PAIR additional=9999\nR 001100f4 00000001 count=1\n"
                                 "C 00000000 00000000 count=2\nTRACE_END\n"
                                 "TRACE_BEGIN version=2 phase=REBAR records=1 encoded_records=1 "
                                 "operations=1 omitted_data_ports=0 dropped=0\n"
                                 "W 00088050 00000006 count=1\nTRACE_END\n")
                    expanded, reports = transform(actual)
                    if len(reports) != 2 or expanded.count("S 00000000 00000064 count=1\n") != 10000:
                        raise RuntimeError(f"{case}: lossless expansion failed")
                else:
                    expected += ("TRACE_SUMMARY version=2 phase=UNLOCK records=20002 operations=20003 "
                                 "omitted_data_ports=2 dropped=0\n"
                                 "TRACE_SUMMARY version=2 phase=REBAR records=1 operations=1 "
                                 "omitted_data_ports=0 dropped=0\n")
                expected += "LOG_FIXTURE_MODE_DONE\n"
                if actual != expected:
                    raise RuntimeError(f"{case}: persisted {filename} has incorrect logging mode or bytes")
                (root / filename).write_text(actual)
            actual = managed_payload(fat.read_file("EFI/40HX/LOGS/L0000007.LOG").decode())
            expected = ("LOG_DETAIL mode=trace requested=false failed=false diagnostic=true\n"
                        "TRACE_BEGIN version=2 phase=UNLOCK records=131072 encoded_records=3 "
                        "operations=131072 omitted_data_ports=0 dropped=1\n"
                        "R 001100f4 00000000 count=1\nS 00000000 00000064 count=1\n"
                        "REPEAT_PAIR additional=65535\nTRACE_END\n")
            if actual != expected or transform(actual)[1][0]["records"] != 131072:
                raise RuntimeError(f"{case}: trace overflow was not promoted or preserved")
            (root / "RZ000007.LOG").write_text(actual)
    return {"case": case, "passed": passed, "seconds": round(time.monotonic() - started, 2),
            "serial_log": str(serial), "qemu_command": command}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--qemu", default=shutil.which("qemu-system-x86_64"))
    parser.add_argument("--ovmf-code")
    parser.add_argument("--ovmf-vars")
    parser.add_argument("--timeout", type=float, default=45)
    parser.add_argument("--image-dir", type=Path, default=WORKSPACE / "target/x86_64-unknown-uefi/release")
    parser.add_argument("--case", action="append", choices=["windows", "unique-windows", "parent",
                        "no-windows", "invalid-windows", "ambiguous-windows", "self-windows",
                        "cpu-full-no-gpu", "cpu-zero-no-gpu", "logging", "failed-parent", "failed-start", "retention", "volume-full", "logcheck", "entrycheck", "entrycheck-invalid",
                        "raw-windows", "raw-corrupt-chain", "raw-ambiguous", "raw-parent", "raw-gpt-windows", "raw-gpt-4k", "raw-gpt-bad-crc",
                        "rebar-enabled", "rebar-disabled", "rebar-invalid", "rebar-literal", "rebar-optout", "rebar-other-volume", "rebar-trace"] + GEN2_CASES)
    parser.add_argument("--unlock", action="store_true", help="also boot FULL/ZERO no-GPU paths")
    parser.add_argument("--adapters", action="store_true", help="test persistent logs and parked failure")
    parser.add_argument("--raw", action="store_true", help="test BlockIo loading with failing SFS")
    parser.add_argument("--rebar", action="store_true", help="test real ReBAR policy reads and fallible memory-map access without a GPU")
    parser.add_argument("--gen2", action="store_true", help="test all Gen2 image entries and --no-gen2 precedence without a GPU")
    parser.add_argument("--output-dir", type=Path, default=WORKSPACE / "target/qemu-smoke")
    args = parser.parse_args()
    if not args.qemu:
        parser.error("qemu-system-x86_64 not found")
    code = firmware(args.ovmf_code, "OVMF_CODE.4m.fd")
    variables = firmware(args.ovmf_vars, "OVMF_VARS.4m.fd")
    binaries = [args.image_dir / name for name in ("cmp40-readonly.efi", "cmp40-fixture-windows.efi",
                                                  "cmp40-fixture-parent.efi")]
    if args.unlock or args.adapters or args.raw or args.rebar or args.gen2 or any(case.startswith(("cpu-", "entrycheck", "rebar-", "gen2-")) or case in ("logging", "failed-parent", "logcheck", "failed-start", "retention", "volume-full") for case in (args.case or [])):
        binaries += [args.image_dir / name for name in ("cmp40-unlock-full.efi", "cmp40-unlock-zero.efi")]
    if args.adapters or any(case in ("logging", "failed-parent", "logcheck", "failed-start", "retention", "volume-full") for case in (args.case or [])):
        binaries += [args.image_dir / name for name in ("cmp40-fixture-log.efi", "cmp40-fixture-failed-parent.efi")]
        binaries += [args.image_dir / "cmp40-logcheck.efi", args.image_dir / "cmp40-fixture-return-error.efi"]
    if args.raw or any(case.startswith("raw-") for case in (args.case or [])):
        binaries += [args.image_dir / "cmp40-fixture-raw.efi"]
    if any(case.startswith("entrycheck") for case in (args.case or [])):
        binaries += [args.image_dir / "cmp40-entrycheck.efi"]
    if args.rebar or args.gen2 or any(case.startswith(("rebar-", "gen2-")) for case in (args.case or [])):
        binaries += [args.image_dir / "cmp40-fixture-rebar-policy.efi"]
    if args.gen2 or any(case.startswith("gen2-") for case in (args.case or [])):
        binaries += [args.image_dir / f"cmp40-gen2-{mode}.efi" for mode in ("prepare", "targets", "retrain")]
    hashes = {str(path): check_efi(path) for path in binaries}
    output = args.output_dir
    output.mkdir(parents=True, exist_ok=True)
    artifacts = Path(tempfile.mkdtemp(prefix="run-", dir=output))
    results = []
    cases = args.case or ["windows", "unique-windows", "parent", "no-windows",
                         "invalid-windows", "ambiguous-windows", "self-windows"]
    if args.unlock and not args.case:
        cases += ["cpu-full-no-gpu", "cpu-zero-no-gpu"]
    if args.adapters and not args.case:
        cases += ["logging", "failed-parent", "logcheck", "failed-start", "retention", "volume-full"]
    if args.raw and not args.case:
        cases += ["raw-windows", "raw-corrupt-chain", "raw-ambiguous", "raw-parent", "raw-gpt-windows", "raw-gpt-4k", "raw-gpt-bad-crc"]
    if args.rebar and not args.case:
        cases += ["rebar-enabled", "rebar-disabled", "rebar-invalid", "rebar-literal", "rebar-optout", "rebar-other-volume", "rebar-trace"]
    if args.gen2 and not args.case:
        cases += GEN2_CASES
    try:
        for case in cases:
            result = run_case(case, artifacts, binaries, args.qemu, code, variables, args.timeout)
            results.append(result)
            print(f"PASS {case}: {result['seconds']}s", flush=True)
    finally:
        report = {"images": hashes, "ovmf_code_sha256": hashlib.sha256(code.read_bytes()).hexdigest(),
                  "ovmf_vars_sha256": hashlib.sha256(variables.read_bytes()).hexdigest(),
                  "qemu_version": subprocess.check_output([args.qemu, "--version"], text=True).splitlines()[0],
                  "complete": len(results) == len(cases), "requested_cases": cases, "results": results}
        (artifacts / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"Artifacts: {artifacts}", flush=True)


if __name__ == "__main__":
    main()

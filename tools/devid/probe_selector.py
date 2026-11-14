# SPDX-License-Identifier: MIT
"""Inspect the Turing XVE Device ID selector; writes require an explicit experiment."""

import argparse
import ctypes
import json
import mmap
import platform
import sys
from contextlib import contextmanager
from pathlib import Path

from read_identity import inspect, read_bar_dword


CYA3_OFFSET = 0x88708
SELECTOR_ENABLE = 1 << 19
SELECTOR_VALUE = 1 << 20
SELECTOR_MASK = SELECTOR_ENABLE | SELECTOR_VALUE


def error_message(error):
    return error.strerror if isinstance(error, OSError) else str(error)


class ExperimentError(ValueError):
    def __init__(self, message, result):
        super().__init__(message)
        self.result = result


def require_unbound(device):
    driver = device / "driver"
    if driver.exists() or driver.is_symlink():
        raise ValueError("the experiment requires an unbound PCI device")
    if (device / "power" / "runtime_status").read_text().strip() != "active":
        raise ValueError("GPU runtime PM is not active; set power/control to on "
                         "before the experiment")


def observe(device):
    before = inspect(device)
    with (device / "resource0").open("rb", buffering=0) as stream:
        value = read_bar_dword(stream, CYA3_OFFSET)
        again = read_bar_dword(stream, CYA3_OFFSET)
    after = inspect(device)
    if value != again or before != after:
        raise ValueError("GPU state changed during selector inspection")
    return {
        "identity": before,
        "xve_cya3": f"{value:08x}",
        "strap_devid_sel_override_enable": bool(value & SELECTOR_ENABLE),
        "strap_devid_sel_override_value": (value >> 20) & 1,
    }


class Register32:
    def __init__(self, cell):
        self.cell = cell

    def read(self):
        value = self.cell.value
        if value == 0xFFFFFFFF or value >> 16 == 0xBADF:
            raise ValueError("selector register became inaccessible")
        return value

    def write(self, value):
        self.cell.value = value


@contextmanager
def writable_register(device):
    base = CYA3_OFFSET & ~(mmap.PAGESIZE - 1)
    with (device / "resource0").open("r+b", buffering=0) as stream:
        with mmap.mmap(stream.fileno(), mmap.PAGESIZE,
                       access=mmap.ACCESS_WRITE, offset=base) as window:
            cell = ctypes.c_uint32.from_buffer(window, CYA3_OFFSET - base)
            register = Register32(cell)
            try:
                yield register
            finally:
                register.cell = None
                del cell


def try_selectors(register, snapshot, guard):
    """Try both one-bit values and restore the original DWORD after each case."""
    guard()
    original = register.read()
    baseline = snapshot()
    if original & SELECTOR_ENABLE:
        raise ValueError("an existing Device ID override is enabled")
    if (baseline["cached_pci_identity"] != "1f0b10de"
            or not baseline["identities_agree"]):
        raise ValueError("the experiment requires a consistent CMP40 identity")

    cases = []
    dirty = False
    failure = None
    try:
        for selector in (0, 1):
            guard()
            if register.read() != original or snapshot() != baseline:
                raise ValueError("GPU state changed before a selector case")
            requested = ((original & ~SELECTOR_MASK) | SELECTOR_ENABLE
                         | (selector << 20))
            case = {"selector": selector,
                    "requested_cya3": f"{requested:08x}"}
            cases.append(case)
            dirty = True
            register.write(requested)
            observed = register.read()
            case.update({
                "observed_cya3": f"{observed:08x}",
                "control_bits_match": (observed & SELECTOR_MASK)
                                      == (requested & SELECTOR_MASK),
                "other_bits_unchanged": (observed & ~SELECTOR_MASK)
                                        == (original & ~SELECTOR_MASK),
            })
            case["identity"] = snapshot()
            register.write(original)
            if register.read() != original:
                raise ValueError("selector register restoration was not confirmed")
            dirty = False
            case["restored_identity"] = snapshot()
            case["identity_restored"] = case["restored_identity"] == baseline
            if not case["identity_restored"]:
                raise ValueError("selector DWORD was restored, but identity differs")
            if not case["control_bits_match"] or not case["other_bits_unchanged"]:
                break
    except (OSError, ValueError, KeyboardInterrupt) as error:
        failure = ("experiment interrupted" if isinstance(error, KeyboardInterrupt)
                   else error_message(error))
    finally:
        if dirty:
            try:
                register.write(original)
                if register.read() != original:
                    raise ValueError("selector restoration failed after an error")
            except (OSError, ValueError) as error:
                failure = f"{failure or 'experiment failed'}; {error_message(error)}"

    result = {"original_cya3": f"{original:08x}", "baseline": baseline,
              "cases": cases}
    try:
        result["final_cya3"] = f"{register.read():08x}"
        result["final_identity"] = snapshot()
        result["restoration_confirmed"] = (
            result["final_cya3"] == result["original_cya3"]
            and result["final_identity"] == baseline)
        if not result["restoration_confirmed"]:
            failure = failure or "final selector or identity restoration was not confirmed"
    except (OSError, ValueError) as error:
        result["restoration_confirmed"] = False
        failure = f"{failure or 'experiment failed'}; {error_message(error)}"
    if failure:
        result["error"] = failure
        raise ExperimentError(failure, result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("device", type=Path, help="PCI device directory")
    parser.add_argument("--experiment", action="store_true",
                        help="try selector values 0 and 1 on an unbound CMP40, then restore")
    args = parser.parse_args()
    if sys.platform != "linux" or platform.machine().lower() not in ("x86_64", "amd64"):
        parser.exit(1, "selector inspection supports only x86-64 Linux\n")
    try:
        if args.experiment:
            require_unbound(args.device)
            initial = inspect(args.device)
            if initial["cached_pci_identity"] != "1f0b10de" or not initial["identities_agree"]:
                raise ValueError("the experiment requires a consistent CMP40 identity")
            with writable_register(args.device) as register:
                result = try_selectors(register, lambda: inspect(args.device),
                                       lambda: require_unbound(args.device))
        else:
            result = observe(args.device)
    except ExperimentError as error:
        print(json.dumps(error.result, indent=2))
        parser.exit(1, f"selector experiment failed: {error}\n")
    except (OSError, ValueError) as error:
        parser.exit(1, f"selector inspection failed: {error_message(error)}\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()

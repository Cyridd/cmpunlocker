# SPDX-License-Identifier: MIT
"""Hold the measured CMP40 selector during a manual stock-driver experiment."""

import argparse
import json
import platform
import sys
import time
from pathlib import Path

from probe_selector import (
    ExperimentError, SELECTOR_ENABLE, SELECTOR_MASK, SELECTOR_VALUE,
    error_message, require_unbound, writable_register,
)
from read_identity import inspect, read_cached_identity


IDENTITY_KEYS = (
    "cached_pci_identity", "config_pci_identity", "bar0_xve_identity", "boot0",
)


def restore_selector(register, snapshot, guard, original, baseline, result):
    """Restore selector bits without overwriting other CYA3 fields."""
    guard()
    current = register.read()
    result["before_restore_cya3"] = f"{current:08x}"
    restore = ((current & ~SELECTOR_MASK) | (original & SELECTOR_MASK))
    result["restore_requested_cya3"] = f"{restore:08x}"
    guard()
    if current != restore:
        register.write(restore)
    final = register.read()
    result["final_cya3"] = f"{final:08x}"
    result["selector_bits_restored"] = (
        final & SELECTOR_MASK) == (original & SELECTOR_MASK)
    result["other_bits_preserved_on_restore"] = (
        final & ~SELECTOR_MASK) == (current & ~SELECTOR_MASK)
    result["final_identity"] = snapshot()
    result["identity_restored"] = all(
        result["final_identity"][key] == baseline[key] for key in IDENTITY_KEYS)
    result["restoration_confirmed"] = (
        result["selector_bits_restored"]
        and result["other_bits_preserved_on_restore"]
        and result["identity_restored"])
    if not result["restoration_confirmed"]:
        raise ValueError("selector or identity restoration was not confirmed")


def hold_selector(register, snapshot, guard, operator_wait, unbound_wait,
                  restore_prepare=None):
    """Select only the measured alternate ID, then restore while unbound."""
    guard()
    original = register.read()
    baseline = snapshot()
    if original & SELECTOR_ENABLE:
        raise ValueError("an existing Device ID override is enabled")
    if (baseline["cached_pci_identity"] != "1f0b10de"
            or not baseline["identities_agree"]):
        raise ValueError("the experiment requires a consistent CMP40 identity")
    requested = ((original & ~SELECTOR_MASK) | SELECTOR_ENABLE | SELECTOR_VALUE)
    result = {"original_cya3": f"{original:08x}", "baseline": baseline,
              "requested_cya3": f"{requested:08x}", "restoration_confirmed": False}
    failure = None
    guard()
    if register.read() != original or snapshot() != baseline:
        raise ValueError("GPU state changed before the selector write")
    guard()
    try:
        register.write(requested)
        selected = register.read()
        result["selected_cya3"] = f"{selected:08x}"
        if selected != requested:
            raise ValueError("selector readback differs; do not load NVIDIA")
        identity = snapshot()
        result["selected_identity"] = identity
        if (identity["cached_pci_identity"] != "1f0b10de"
                or identity["config_pci_identity"] != "1f4b10de"
                or identity["bar0_xve_identity"] != "1f4b10de"
                or identity["boot0"] != baseline["boot0"]):
            raise ValueError("the measured 1f4b identity was not reproduced; "
                             "do not load NVIDIA")
        operator_wait(result)
    except (OSError, ValueError, KeyboardInterrupt, EOFError) as error:
        failure = ("experiment interrupted" if isinstance(error, KeyboardInterrupt)
                   else "terminal input ended" if isinstance(error, EOFError)
                   else error_message(error))
    finally:
        try:
            unbound_wait()
            if restore_prepare is not None:
                restore_prepare(result)
            restore_selector(register, snapshot, guard, original, baseline, result)
        except (OSError, ValueError, KeyboardInterrupt) as error:
            detail = ("restoration interrupted" if isinstance(error, KeyboardInterrupt)
                      else error_message(error))
            failure = f"{failure or 'experiment failed'}; {detail}"
    if failure:
        result["error"] = failure
        raise ExperimentError(failure, result)
    return result


def wait_for_operator(result):
    print(json.dumps({"state": "SELECTOR_HELD", **result}, indent=2),
          file=sys.stderr, flush=True)
    print("Keep this terminal open. In a second terminal, run the prepared "
          "stock-driver checks, then close NVIDIA clients and unload NVIDIA. "
          "Press Enter here after unloading to restore the selector.",
          file=sys.stderr, flush=True)
    input()


def wait_for_unbound(device):
    announced = False
    while (device / "driver").exists() or (device / "driver").is_symlink():
        if not announced:
            print("NVIDIA is still bound. No restoration write will run. "
                  "Unload it without force in the second terminal; waiting.",
                  file=sys.stderr, flush=True)
            announced = True
        try:
            time.sleep(1)
        except KeyboardInterrupt:
            continue


def require_no_driver(device):
    if (device / "driver").exists() or (device / "driver").is_symlink():
        raise ValueError("restoration requires an unbound PCI device")


def prepare_restore_power(device, result):
    """Reassert runtime power after unbinding, before any restoration MMIO."""
    require_no_driver(device)
    control = device / "power" / "control"
    status = device / "power" / "runtime_status"
    result["runtime_pm_before_restore"] = {
        "control": control.read_text().strip(), "status": status.read_text().strip(),
    }
    require_no_driver(device)
    control.write_text("on\n")
    result["runtime_pm_on_reasserted"] = True
    deadline = time.monotonic() + 5
    while status.read_text().strip() != "active":
        require_no_driver(device)
        if time.monotonic() >= deadline:
            raise ValueError("runtime PM did not become active; no restoration MMIO ran")
        time.sleep(0.05)
    require_unbound(device)


def saved_restore_state(saved):
    """Accept only an original CMP40 baseline with override disabled."""
    try:
        text = saved["original_cya3"]
        baseline = saved["baseline"]
        if not isinstance(text, str) or len(text) != 8 or any(
                c not in "0123456789abcdefABCDEF" for c in text):
            raise ValueError("invalid original CYA3")
        original = int(text, 16)
        if (original == 0xFFFFFFFF or original >> 16 == 0xBADF
                or original & SELECTOR_ENABLE):
            raise ValueError("invalid original CYA3")
        if (any(baseline[key] != "1f0b10de" for key in IDENTITY_KEYS[:3])
                or baseline["boot0"] != "166000a1"
                or baseline["identities_agree"] is not True):
            raise ValueError("invalid original CMP40 identity")
    except (KeyError, TypeError, ValueError) as error:
        raise ValueError("saved state must contain an original CMP40 baseline "
                         "with override disabled") from error
    return original, baseline


def restore_saved(device, saved):
    original, baseline = saved_restore_state(saved)
    result = {"mode": "restore_saved", "original_cya3": f"{original:08x}",
              "baseline": baseline, "restoration_confirmed": False}
    try:
        require_no_driver(device)
        if read_cached_identity(device) != (0x10DE, 0x1F0B):
            raise ValueError("restoration requires the original cached CMP40 identity")
        prepare_restore_power(device, result)
        identity = inspect(device)
        if (identity["cached_pci_identity"] != baseline["cached_pci_identity"]
                or identity["boot0"] != baseline["boot0"]
                or identity["config_pci_identity"] != identity["bar0_xve_identity"]
                or identity["config_pci_identity"] not in ("1f0b10de", "1f4b10de")):
            raise ValueError("live identity is outside the measured CMP40 states")
        result["identity_before_restore"] = identity
        require_unbound(device)
        with writable_register(device) as register:
            current = register.read()
            expected = (SELECTOR_ENABLE | SELECTOR_VALUE
                        if identity["config_pci_identity"] == "1f4b10de"
                        else original & SELECTOR_MASK)
            if (current & SELECTOR_MASK) != expected:
                raise ValueError("live selector does not match the saved or measured state")
            restore_selector(register, lambda: inspect(device),
                             lambda: require_unbound(device), original, baseline, result)
    except (OSError, ValueError, KeyboardInterrupt) as error:
        detail = ("restoration interrupted" if isinstance(error, KeyboardInterrupt)
                  else error_message(error))
        result["error"] = detail
        raise ExperimentError(detail, result) from error
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("device", type=Path, help="PCI device directory")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--experiment", action="store_true",
                        help="enable the selector-1 hold; requires a second terminal")
    mode.add_argument("--restore-from", type=Path, metavar="JSON",
                      help="restore selector bits from a saved CMP40 baseline while unbound")
    args = parser.parse_args()
    if not args.experiment and args.restore_from is None:
        parser.exit(1, "select --experiment or --restore-from\n")
    if sys.platform != "linux" or platform.machine().lower() not in ("x86_64", "amd64"):
        parser.exit(1, "selector hold supports only x86-64 Linux\n")
    if args.experiment and not sys.stdin.isatty():
        parser.exit(1, "selector hold requires interactive terminal input\n")
    try:
        if args.restore_from is not None:
            saved = json.loads(args.restore_from.read_text())
            result = restore_saved(args.device, saved)
        else:
            require_unbound(args.device)
            initial = inspect(args.device)
            if initial["cached_pci_identity"] != "1f0b10de" or not initial["identities_agree"]:
                raise ValueError("the experiment requires a consistent CMP40 identity")
            with writable_register(args.device) as register:
                result = hold_selector(
                    register, lambda: inspect(args.device),
                    lambda: require_unbound(args.device), wait_for_operator,
                    lambda: wait_for_unbound(args.device),
                    lambda result: prepare_restore_power(args.device, result))
    except ExperimentError as error:
        print(json.dumps(error.result, indent=2))
        parser.exit(1, f"selector hold failed: {error}\n")
    except (OSError, ValueError) as error:
        parser.exit(1, f"selector hold failed: {error_message(error)}\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()

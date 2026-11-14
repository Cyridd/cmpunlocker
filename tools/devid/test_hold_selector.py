# SPDX-License-Identifier: MIT
"""Offline restoration and guard checks; these tests never access a GPU."""

import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import MagicMock, patch

import hold_selector as hold
import probe_selector as probe
import read_identity


BASELINE = {
    "cached_pci_identity": "1f0b10de", "config_pci_identity": "1f0b10de",
    "bar0_xve_identity": "1f0b10de", "boot0": "166000a1",
    "pci_command": "0003", "identities_agree": True,
}


class Register:
    def __init__(self, value=0x0987E0ED):
        self.value = value
        self.writes = []

    def read(self):
        return self.value

    def write(self, value):
        self.writes.append(value)
        self.value = value


def snapshot(register):
    if register.value & probe.SELECTOR_ENABLE:
        return dict(BASELINE, config_pci_identity="1f4b10de",
                    bar0_xve_identity="1f4b10de", identities_agree=False)
    return dict(BASELINE)


class HoldTests(unittest.TestCase):
    def test_unload_power_transition_is_prepared_before_restoration(self):
        register = Register()
        state = {"active": True, "unbound_waited": False}

        def guard():
            if not state["active"]:
                raise ValueError("runtime PM is inactive")

        def operator_wait(_):
            state["active"] = False

        def unbound_wait():
            state["unbound_waited"] = True

        def prepare(result):
            self.assertTrue(state["unbound_waited"])
            self.assertEqual(register.writes, [0x099FE0ED])
            state["active"] = True
            result["runtime_pm_on_reasserted"] = True

        result = hold.hold_selector(register, lambda: snapshot(register), guard,
                                    operator_wait, unbound_wait, prepare)
        self.assertTrue(result["restoration_confirmed"])
        self.assertEqual(register.writes, [0x099FE0ED, 0x0987E0ED])

    def test_cli_validates_identity_before_opening_writable_bar(self):
        for inspection in (ValueError("unsupported GPU"),
                           dict(BASELINE, cached_pci_identity="1f4b10de")):
            with self.subTest(inspection=inspection):
                with (patch.object(hold.sys, "argv", ["hold_selector.py", "unused", "--experiment"]),
                      patch.object(hold.sys.stdin, "isatty", return_value=True),
                      patch.object(hold, "require_unbound"),
                      patch.object(hold, "inspect", side_effect=inspection if isinstance(inspection, Exception) else None,
                                   return_value=inspection),
                      patch.object(hold, "writable_register") as writable,
                      patch.object(hold.sys, "stderr")):
                    with self.assertRaises(SystemExit) as caught:
                        hold.main()
                    self.assertEqual(caught.exception.code, 1)
                    writable.assert_not_called()

    def test_no_restore_write_while_driver_is_bound(self):
        register = Register()
        state = {"bound": False, "waited": False}

        def guard():
            if state["bound"]:
                raise ValueError("driver bound")

        def operator_wait(result):
            self.assertEqual(result["selected_identity"]["config_pci_identity"], "1f4b10de")
            state["bound"] = True

        def unbound_wait():
            self.assertEqual(register.writes, [0x099FE0ED])
            state.update(bound=False, waited=True)

        result = hold.hold_selector(register, lambda: snapshot(register), guard,
                                    operator_wait, unbound_wait)
        self.assertTrue(state["waited"])
        self.assertTrue(result["restoration_confirmed"])
        self.assertEqual(register.writes, [0x099FE0ED, 0x0987E0ED])

    def test_restoration_preserves_driver_changes_to_other_bits(self):
        register = Register()

        def operator_wait(_):
            register.value ^= 0x100

        result = hold.hold_selector(register, lambda: snapshot(register), lambda: None,
                                    operator_wait, lambda: None)
        self.assertEqual(register.value, 0x0987E1ED)
        self.assertTrue(result["restoration_confirmed"])

    def test_preflight_refuses_alternate_cached_identity_and_existing_override(self):
        alternate = dict(BASELINE, cached_pci_identity="1f4b10de",
                         config_pci_identity="1f4b10de", bar0_xve_identity="1f4b10de")
        for value, identity in ((0x0987E0ED, alternate),
                                (0x0987E0ED, dict(BASELINE, identities_agree=False)),
                                (0x099FE0ED, BASELINE)):
            with self.subTest(value=value, identity=identity):
                register = Register(value)
                with self.assertRaises(ValueError):
                    hold.hold_selector(register, lambda: identity, lambda: None,
                                       lambda _: self.fail("operator wait reached"), lambda: None)
                self.assertEqual(register.writes, [])
                with self.assertRaises(ValueError):
                    probe.try_selectors(register, lambda: identity, lambda: None)
                self.assertEqual(register.writes, [])

    def test_bound_device_refuses_initial_write(self):
        register = Register()

        def guard():
            raise ValueError("driver bound")

        with self.assertRaises(ValueError):
            hold.hold_selector(register, lambda: snapshot(register), guard,
                               lambda _: None, lambda: None)
        self.assertEqual(register.writes, [])

    def test_rejected_write_and_unexpected_identity_never_reach_operator(self):
        class RejectWrite(Register):
            def write(self, value):
                self.writes.append(value)

        for register in (RejectWrite(), Register()):
            with self.subTest(register=type(register).__name__):
                def wrong_identity():
                    value = snapshot(register)
                    if not value["identities_agree"]:
                        value.update(config_pci_identity="1f0610de", bar0_xve_identity="1f0610de")
                    return value
                with self.assertRaises(probe.ExperimentError) as caught:
                    hold.hold_selector(register, wrong_identity, lambda: None,
                                       lambda _: self.fail("operator wait reached"), lambda: None)
                self.assertTrue(caught.exception.result["restoration_confirmed"])
                self.assertEqual(register.value, 0x0987E0ED)

    def test_interrupt_and_eof_attempt_restoration(self):
        for exception in (KeyboardInterrupt, EOFError):
            register = Register()

            def operator_wait(_):
                raise exception()

            with self.assertRaises(probe.ExperimentError) as caught:
                hold.hold_selector(register, lambda: snapshot(register), lambda: None,
                                   operator_wait, lambda: None)
            self.assertTrue(caught.exception.result["restoration_confirmed"])
            self.assertEqual(register.value, 0x0987E0ED)

    def test_release_guard_failure_leaves_selector_untouched(self):
        register = Register()
        state = {"bound": False}

        def guard():
            if state["bound"]:
                raise ValueError("driver bound")

        def operator_wait(_):
            state["bound"] = True

        with self.assertRaises(probe.ExperimentError) as caught:
            hold.hold_selector(register, lambda: snapshot(register), guard,
                               operator_wait, lambda: None)
        self.assertFalse(caught.exception.result["restoration_confirmed"])
        self.assertEqual(register.writes, [0x099FE0ED])

    def test_restore_rejection_reports_unconfirmed_identity(self):
        class RejectRestore(Register):
            def write(self, value):
                self.writes.append(value)
                if value & probe.SELECTOR_ENABLE:
                    self.value = value

        register = RejectRestore()
        with self.assertRaises(probe.ExperimentError) as caught:
            hold.hold_selector(register, lambda: snapshot(register), lambda: None,
                               lambda _: None, lambda: None)
        self.assertFalse(caught.exception.result["restoration_confirmed"])

    def test_driver_reset_already_restored_selector_needs_no_write(self):
        register = Register()

        def operator_wait(_):
            register.value = 0x0987E0ED

        result = hold.hold_selector(register, lambda: snapshot(register), lambda: None,
                                    operator_wait, lambda: None)
        self.assertTrue(result["restoration_confirmed"])
        self.assertEqual(register.writes, [0x099FE0ED])

    def test_pci_command_change_does_not_hide_identity_restoration(self):
        register = Register()

        def identity():
            return dict(snapshot(register), pci_command="0407" if len(register.writes) > 1 else "0003")

        result = hold.hold_selector(register, identity, lambda: None, lambda _: None, lambda: None)
        self.assertTrue(result["restoration_confirmed"])
        self.assertEqual(result["final_identity"]["pci_command"], "0407")


class ReadIdentityTests(unittest.TestCase):
    def test_cached_alternate_identity_is_accepted_for_read_only_inspection(self):
        device = MagicMock()
        device.__truediv__.return_value.__truediv__.return_value.read_text.return_value = "active"
        with (patch.object(read_identity, "read_cached_identity", return_value=(0x10DE, 0x1F4B)),
              patch.object(read_identity, "read_config", return_value=(0x1F4B10DE, 3)),
              patch.object(read_identity, "read_bar_dword", side_effect=[0x166000A1, 0x1F4B10DE, 0x1F4B10DE])):
            result = read_identity.inspect(device)
        self.assertTrue(result["identities_agree"])
        self.assertEqual(result["cached_pci_identity"], "1f4b10de")


class RestoreTests(unittest.TestCase):
    def test_runtime_pm_is_reasserted_after_unbinding(self):
        with TemporaryDirectory() as folder:
            device = Path(folder)
            (device / "power").mkdir()
            control = device / "power" / "control"
            status = device / "power" / "runtime_status"
            control.write_text("auto\n")
            status.write_text("suspended\n")
            result = {}
            with patch.object(hold.time, "sleep", side_effect=lambda _: status.write_text("active\n")):
                hold.prepare_restore_power(device, result)
            self.assertEqual(control.read_text(), "on\n")
            self.assertEqual(result["runtime_pm_before_restore"], {"control": "auto", "status": "suspended"})
            self.assertTrue(result["runtime_pm_on_reasserted"])

    def test_bound_device_prevents_power_policy_write(self):
        with TemporaryDirectory() as folder:
            device = Path(folder)
            (device / "driver").mkdir()
            (device / "power").mkdir()
            control = device / "power" / "control"
            control.write_text("auto\n")
            with self.assertRaises(ValueError):
                hold.prepare_restore_power(device, {})
            self.assertEqual(control.read_text(), "auto\n")

    def test_power_timeout_reports_failure_before_mmio(self):
        with TemporaryDirectory() as folder:
            device = Path(folder)
            (device / "power").mkdir()
            (device / "power" / "control").write_text("auto\n")
            (device / "power" / "runtime_status").write_text("suspended\n")
            with patch.object(hold.time, "monotonic", side_effect=[0, 6]):
                with self.assertRaisesRegex(ValueError, "no restoration MMIO ran"):
                    hold.prepare_restore_power(device, {})

    def test_invalid_saved_state_refuses_power_preparation(self):
        states = [[], {}, {"original_cya3": "099fe0ed", "baseline": BASELINE},
                  {"original_cya3": "0987e0ed", "baseline": dict(BASELINE, config_pci_identity="1f4b10de")}]
        for state in states:
            with self.subTest(state=state), patch.object(hold, "prepare_restore_power") as prepare:
                with self.assertRaises(ValueError):
                    hold.restore_saved(MagicMock(), state)
                prepare.assert_not_called()

    def test_saved_baseline_recovers_the_measured_alternate_identity(self):
        register = Register(0x099FE0ED)
        saved = {"original_cya3": "0987e0ed", "baseline": BASELINE,
                 "restoration_confirmed": False, "error": "runtime PM was inactive"}
        with (patch.object(hold, "require_no_driver"),
              patch.object(hold, "read_cached_identity", return_value=(0x10DE, 0x1F0B)),
              patch.object(hold, "prepare_restore_power"),
              patch.object(hold, "require_unbound"),
              patch.object(hold, "inspect", side_effect=lambda _: snapshot(register)),
              patch.object(hold, "writable_register") as writable):
            writable.return_value.__enter__.return_value = register
            result = hold.restore_saved(MagicMock(), saved)
        self.assertTrue(result["restoration_confirmed"])
        self.assertEqual(register.writes, [0x0987E0ED])

    def test_unknown_live_identity_refuses_writable_bar(self):
        saved = {"original_cya3": "0987e0ed", "baseline": BASELINE}
        with (patch.object(hold, "require_no_driver"),
              patch.object(hold, "read_cached_identity", return_value=(0x10DE, 0x1F0B)),
              patch.object(hold, "prepare_restore_power"),
              patch.object(hold, "inspect", return_value=dict(BASELINE, config_pci_identity="1f0610de")),
              patch.object(hold, "writable_register") as writable):
            with self.assertRaises(probe.ExperimentError) as caught:
                hold.restore_saved(MagicMock(), saved)
            writable.assert_not_called()
        self.assertFalse(caught.exception.result["restoration_confirmed"])

    def test_selector_identity_disagreement_refuses_restoration_write(self):
        register = Register(0x098FE0ED)
        saved = {"original_cya3": "0987e0ed", "baseline": BASELINE}
        with (patch.object(hold, "require_no_driver"),
              patch.object(hold, "read_cached_identity", return_value=(0x10DE, 0x1F0B)),
              patch.object(hold, "prepare_restore_power"),
              patch.object(hold, "require_unbound"),
              patch.object(hold, "inspect", side_effect=lambda _: snapshot(register)),
              patch.object(hold, "writable_register") as writable):
            writable.return_value.__enter__.return_value = register
            with self.assertRaises(probe.ExperimentError) as caught:
                hold.restore_saved(MagicMock(), saved)
        self.assertFalse(caught.exception.result["restoration_confirmed"])
        self.assertEqual(register.writes, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)

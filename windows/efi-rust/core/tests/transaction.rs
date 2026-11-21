// SPDX-License-Identifier: MIT
use std::fmt::Write as _;
use std::io::Write as _;
use std::process::{Command, Stdio};

use cmp40_efi_core::state::Control;
use cmp40_efi_core::transaction::{
    self, reg::*, BooterCheck, Error, Fwsec, Phase, Prepared, Transaction, FWSEC_BYTES,
};

mod support;
use cmp40_efi_core::transaction::Io as _;
use support::{assets, canonical, Event, Model};

fn status(result: Result<(), Error>) -> u32 {
    match result {
        Ok(()) => 0,
        Err(Error::NotReady) => 6,
        Err(Error::Device) => 7,
        Err(Error::Timeout) => 18,
        Err(other) => panic!("unexpected comparison status: {other:?}"),
    }
}

fn reference(mode: &str, control: Control, input: &str) -> Vec<u8> {
    let mut child = Command::new(std::env::var("PYTHON").unwrap_or_else(|_| "python3".into()))
        .args([
            concat!(
                env!("CARGO_MANIFEST_DIR"),
                "/tests/c_execution_reference.py"
            ),
            mode,
            if control == Control::Full {
                "full"
            } else {
                "zero"
            },
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("Python and a native C compiler are needed for execution parity");
    child
        .stdin
        .take()
        .unwrap()
        .write_all(input.as_bytes())
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(
        output.status.success(),
        "C execution adapter failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    output.stdout
}

fn compare_trace(label: &str, c: &[Event], rust: &[Event]) {
    let c = canonical(c);
    let rust = canonical(rust);
    let limit = c.len().min(rust.len());
    for i in 0..limit {
        assert_eq!(
            c[i],
            rust[i],
            "{label}: event {i}, preceding C events {:x?}, Rust {:x?}",
            &c[i.saturating_sub(4)..i],
            &rust[i.saturating_sub(4)..i]
        );
    }
    assert_eq!(
        c.len(),
        rust.len(),
        "{label}: event count; C tail {:x?}, Rust tail {:x?}",
        &c[limit.saturating_sub(3)..],
        &rust[limit.saturating_sub(3)..]
    );
}

fn compare(mode: &str, control: Control, faults: &[u32]) {
    let mut input = String::new();
    let mut cases = Vec::new();
    for address in [0x120000_u64, 0x110bb0000, 0x1c0020000] {
        for &fault in faults {
            writeln!(
                input,
                "{fault} {} {address}",
                u8::from(control == Control::Zero)
            )
            .unwrap();
            cases.push((address, fault));
        }
    }
    let output = reference(mode, control, &input);
    let mut position = 0;
    for (address, fault) in cases {
        let mut c_events = Vec::new();
        let c_status = loop {
            let row: Event = std::array::from_fn(|i| {
                u32::from_le_bytes(
                    output[position + i * 4..position + i * 4 + 4]
                        .try_into()
                        .unwrap(),
                )
            });
            position += 12;
            if row[0] == 4 {
                break row[1];
            }
            c_events.push(row);
        };
        let patched = &output[position..position + FWSEC_BYTES];
        position += FWSEC_BYTES;
        let (mut fwsec, loader, booter) = assets();
        let mut model = Model::new(fault, control);
        let result = match mode {
            "prepare" => transaction::prepare_engines(&mut model),
            "fwsec" => transaction::boot_fwsec(&mut model, &mut fwsec, address, &loader),
            "booter" => transaction::boot_sec2(&mut model, &booter, address),
            "finish" => {
                model.completed();
                transaction::check_completion(&mut model, control)
            }
            _ => unreachable!(),
        };
        let label = format!("{mode} {control:?} fault={fault} DMA={address:x}");
        assert_eq!(c_status, status(result), "{label}");
        assert_eq!(patched, fwsec, "{label}: FWSEC mutation");
        if mode == "finish" {
            // The core stops at the verdict. C then seals output and either
            // starts Windows or returns the error. Verify its terminal tail.
            let seal = c_events.iter().position(|row| row[0] == 5).unwrap();
            let expected: &[Event] = if c_status == 0 {
                &[[5, 0, 0], [6, 0, 0]]
            } else {
                &[[5, 0, 0]]
            };
            assert_eq!(&c_events[seal..], expected, "{label}: C terminal handoff");
            c_events.truncate(seal);
        }
        if mode == "prepare" {
            if let Some(seal) = c_events.iter().position(|row| row[0] == 5) {
                assert_ne!(c_status, 0);
                assert_eq!(&c_events[seal..], &[[5, 0, 0]]);
                c_events.truncate(seal);
            }
        }
        compare_trace(&label, &c_events, &model.events);
    }
    assert_eq!(position, output.len());
}

#[test]
fn gfw_stop_and_conditional_falcon_switch_match_the_c_entry_segment() {
    compare("prepare", Control::Full, &[0, 26, 29, 30, 31]);
}

#[test]
fn fwsec_with_loader_matches_c_under_reset_geometry_dma_and_wpr_failures() {
    compare("fwsec", Control::Full, &[0, 1, 2, 3, 4, 5, 6, 25]);
}

#[test]
fn booter_port_load_readbacks_and_sparse_completion_match_c() {
    compare("booter", Control::Full, &[0, 7, 8, 9, 10, 11, 12, 13]);
}

#[test]
fn each_booter_failure_reports_the_already_observed_gate_without_retry() {
    for (fault, check, register, observed) in [
        (7, BooterCheck::PreloadHalt, SEC2 + CPU, 0),
        (8, BooterCheck::NonSecureCode, SEC2 + IMEMD, 0x36251402),
        (
            9,
            BooterCheck::ProductionSignature,
            SEC2 + DMEMD,
            0x36251402,
        ),
        (32, BooterCheck::BootVector, SEC2 + BOOTVEC, 4),
        (10, BooterCheck::MetadataLow, SEC2 + MBOX0, 0x91),
        (11, BooterCheck::MetadataHigh, SEC2 + MBOX1, 0),
        (12, BooterCheck::MailboxStatus, SEC2 + MBOX0, 0x31),
        (13, BooterCheck::CompletionHalt, SEC2 + CPU, 0),
    ] {
        let (_, _, booter) = assets();
        let mut model = Model::new(fault, Control::Zero);
        assert!(transaction::boot_sec2(&mut model, &booter, 0x117002000).is_err());
        assert_eq!(model.failures.len(), 1, "fault {fault}");
        let report = model.failures[0];
        assert_eq!(
            (report.check, report.register, report.observed),
            (check, register, observed),
            "fault {fault}"
        );
        assert!(model.events.contains(&[0, register, observed]));
        let starts = model
            .events
            .iter()
            .filter(|event| **event == [1, SEC2 + CPU, 2])
            .count();
        assert_eq!(starts, usize::from(fault == 12 || fault == 13));
    }
}

#[test]
fn native_finish_matches_c_for_full_and_zero_under_faults() {
    for control in [Control::Full, Control::Zero] {
        compare(
            "finish",
            control,
            &[0, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 27, 28],
        );
    }
}

#[test]
fn whole_prepared_transaction_orders_fwsec_booter_and_native_finish_without_retry() {
    for control in [Control::Full, Control::Zero] {
        for (fault, expected, phase) in [
            (0, Ok(()), Phase::Ready),
            (1, Ok(()), Phase::Ready),
            (26, Ok(()), Phase::Ready),
            (29, Err(Error::NotReady), Phase::GfwStopped),
            (30, Err(Error::Device), Phase::GfwStopped),
            (31, Err(Error::Device), Phase::GfwStopped),
            (2, Err(Error::NotReady), Phase::Fwsec),
            (6, Err(Error::NotReady), Phase::Fwsec),
            (8, Err(Error::Device), Phase::Booter),
            (12, Err(Error::Device), Phase::Booter),
            (13, Err(Error::NotReady), Phase::Booter),
            (15, Err(Error::Timeout), Phase::Completion),
            (16, Err(Error::NotReady), Phase::Completion),
            (20, Err(Error::NotReady), Phase::Completion),
        ] {
            let (mut fwsec, loader, booter) = assets();
            let mut prepared = Prepared {
                fwsec: &mut fwsec,
                fwsec_dma: 0x118000000,
                loader: &loader,
                booter: &booter,
                metadata_dma: 0x117000000,
            };
            let mut model = Model::new(fault, control);
            let mut tx = Transaction::new();
            assert_eq!(
                tx.run_prepared(&mut model, &mut prepared, control),
                expected,
                "{control:?} fault={fault}"
            );
            assert_eq!(
                tx.phase(),
                if expected.is_ok() {
                    Phase::Ready
                } else {
                    Phase::Failed
                }
            );
            assert_eq!(tx.failed_at(), expected.err().map(|_| phase));
            let gsp_starts: Vec<_> = model
                .events
                .iter()
                .enumerate()
                .filter(|(_, e)| **e == [1, GSP + CPU, 2])
                .map(|(i, _)| i)
                .collect();
            let sec_starts: Vec<_> = model
                .events
                .iter()
                .enumerate()
                .filter(|(_, e)| **e == [1, SEC2 + CPU, 2])
                .map(|(i, _)| i)
                .collect();
            assert!(gsp_starts.len() <= 1 && sec_starts.len() <= 1);
            if let Some(&start) = sec_starts.first() {
                assert_eq!(gsp_starts.len(), 1);
                assert!(gsp_starts[0] < start);
                assert_eq!(model.events[start + 1], [2, 3_000_000, 0]);
                assert_eq!(model.events[start + 2][..2], [0, SEC2 + CPU]);
                assert_eq!(model.events[start + 3], [2, 1_000_000, 0]);
                assert!(model.events[start + 1..].iter().all(|e| e[0] != 1));
                assert!(model.events[start + 1..].iter().all(|e| e[0] != 3));
            }
            // No SEC2 reset, host selector/PLM stores, or additional GSP reset
            // can be smuggled into the tested orchestration.
            for e in &model.events {
                assert!(!(e[0] == 1 && [SEC2 + ENGINE, SS0, SS1, FEAT_PLM].contains(&e[1])));
            }
            let saved = model.events.clone();
            assert_eq!(
                tx.run_prepared(&mut model, &mut prepared, control),
                Err(Error::AlreadyAttempted)
            );
            assert_eq!(model.events, saved, "reuse must fail before any IO");
        }
    }
}

#[test]
fn malformed_prepared_buffers_fail_before_any_io() {
    for kind in 0..6 {
        let (mut fwsec, mut loader, mut booter) = assets();
        let mut fwsec_dma = 0x118000000;
        let mut metadata_dma = 0x117000000;
        match kind {
            0 => {
                fwsec.pop();
            }
            1 => {
                loader.pop();
            }
            2 => {
                booter.pop();
            }
            3 => fwsec_dma += 1,
            4 => metadata_dma = 0,
            5 => fwsec_dma = (1 << 49) - 4096,
            _ => unreachable!(),
        }
        let mut model = Model::new(0, Control::Full);
        let mut tx = Transaction::new();
        assert_eq!(
            tx.run_prepared(
                &mut model,
                &mut Prepared {
                    fwsec: &mut fwsec,
                    fwsec_dma,
                    metadata_dma,
                    loader: &loader,
                    booter: &booter
                },
                Control::Full
            ),
            Err(Error::InvalidInput)
        );
        assert!(model.events.is_empty());
        assert_eq!(tx.phase(), Phase::Failed);
    }
}

#[test]
fn deferred_fwsec_preparation_runs_once_at_the_c_allocation_point() {
    for failure in [0, 1, 2] {
        let (mut fwsec, loader, booter) = assets();
        let mut model = Model::new(0, Control::Full);
        let mut tx = Transaction::new();
        let result = tx.run(
            &mut model,
            Control::Full,
            0x117000000,
            |io| {
                io.events.push([7, 0, 0]); // Synthetic allocation/copy marker.
                assert_eq!(io.events[0], [1, GSP + ENGINE, 1]);
                assert!(io.events.iter().all(|e| *e != [1, GSP + CPU, 2]));
                if failure == 1 {
                    return Err(Error::Device);
                }
                transaction::Io::flush(io);
                Ok(Fwsec {
                    bytes: &mut fwsec,
                    dma: 0x118000000,
                    loader: &loader,
                })
            },
            |io| {
                io.events.push([8, 0, 0]);
                assert!(io.events.contains(&[1, GSP + CPU, 2]));
                assert!(io.events.iter().all(|e| *e != [1, SEC2 + CPU, 2]));
                if failure == 2 {
                    return Err(Error::Device);
                }
                transaction::Io::flush(io);
                Ok(&booter)
            },
        );
        assert_eq!(
            result,
            if failure != 0 {
                Err(Error::Device)
            } else {
                Ok(())
            }
        );
        let marker = model.events.iter().position(|e| e[0] == 7).unwrap();
        assert_eq!(model.events.iter().filter(|e| e[0] == 7).count(), 1);
        if failure == 1 {
            assert_eq!(marker + 1, model.events.len());
            assert_eq!(tx.failed_at(), Some(Phase::Fwsec));
            assert!(model.events.iter().all(|e| e[0] != 8));
        } else {
            assert_eq!(model.events[marker + 1], [3, 0, 0]);
            assert_eq!(model.events[marker + 2], [1, GSP + 0x80, 0]);
            let booter_marker = model.events.iter().position(|e| e[0] == 8).unwrap();
            assert_eq!(model.events.iter().filter(|e| e[0] == 8).count(), 1);
            if failure == 2 {
                assert_eq!(tx.failed_at(), Some(Phase::Booter));
                assert_eq!(booter_marker + 1, model.events.len());
            } else {
                assert_eq!(model.events[booter_marker + 1], [3, 0, 0]);
                assert_eq!(model.events[booter_marker + 2][..2], [0, SEC2 + HWCFG2]);
            }
        }
        let saved = model.events.clone();
        assert_eq!(
            tx.run(
                &mut model,
                Control::Full,
                0x117000000,
                |_| panic!("must not prepare twice"),
                |_| panic!("must not prepare Booter twice")
            ),
            Err(Error::AlreadyAttempted)
        );
        assert_eq!(model.events, saved);
    }
}

/// WPR2 can appear before FWSEC stops, as in the physical ZERO trace.
struct CompletingFwsec {
    model: Model,
    delay_ticks: u32,
    ticks: u32,
    started: bool,
    sec_started: bool,
    finish_cpu: u32,
    scratch: u32,
    finish_lo: u32,
    finish_hi: u32,
    results: Vec<transaction::FwsecResult>,
}
impl CompletingFwsec {
    fn new(delay_ticks: u32) -> Self {
        Self {
            model: Model::new(0, Control::Zero),
            delay_ticks,
            ticks: 0,
            started: false,
            sec_started: false,
            finish_cpu: 0x10,
            scratch: 0,
            finish_lo: cmp40_efi_core::state::WPR2_LO_UP,
            finish_hi: cmp40_efi_core::state::WPR2_HI_UP,
            results: Vec::new(),
        }
    }
}
impl transaction::Io for CompletingFwsec {
    fn read32(&mut self, offset: u32) -> u32 {
        if self.started
            && !self.sec_started
            && offset == GSP + CPU
            && self.ticks >= self.delay_ticks
        {
            self.model.regs.insert(GSP + CPU, self.finish_cpu);
            self.model.regs.insert(0x1438, self.scratch);
            self.model.regs.insert(WPR_LO, self.finish_lo);
            self.model.regs.insert(WPR_HI, self.finish_hi);
        }
        self.model.read32(offset)
    }
    fn write32(&mut self, offset: u32, value: u32) {
        if offset == SEC2 + CPU && value == 2 {
            assert_eq!(
                self.model.regs[&(GSP + CPU)] & 0x10,
                0x10,
                "Booter must not start while FWSEC runs"
            );
            self.sec_started = true;
        }
        self.model.write32(offset, value);
        if offset == GSP + CPU && value == 2 {
            self.started = true;
            self.model.regs.insert(GSP + CPU, 0);
        }
    }
    fn stall(&mut self, us: u32) {
        self.model.stall(us);
        if self.started && !self.sec_started && us == 1000 {
            self.ticks += 1;
        }
    }
    fn flush(&mut self) {
        self.model.flush();
    }
    fn fwsec_result(&mut self, result: transaction::FwsecResult) {
        self.results.push(result);
    }
}

#[test]
fn wpr_appearance_waits_for_fwsec_halt_and_result_before_preparing_booter() {
    for delay in [0, 1, 3, 50] {
        let (mut fwsec, loader, booter) = assets();
        let mut io = CompletingFwsec::new(delay);
        let mut tx = Transaction::new();
        assert_eq!(
            tx.run(
                &mut io,
                Control::Zero,
                0x117000000,
                |io| {
                    io.flush();
                    Ok(Fwsec {
                        bytes: &mut fwsec,
                        dma: 0x118000000,
                        loader: &loader,
                    })
                },
                |io| {
                    assert_eq!(io.results.len(), 1);
                    assert_eq!(io.results[0].cpuctl & 0x10, 0x10);
                    assert!(io.results[0].registers.is_some());
                    io.flush();
                    Ok(&booter)
                }
            ),
            Ok(())
        );
        assert_eq!(tx.phase(), Phase::Ready);
        assert_eq!(io.results.len(), 1);
        assert_eq!(io.results[0].waited_us, delay * 1000);
        assert_eq!(io.results[0].registers.unwrap().scratch0e, 0);
        assert!(io.sec_started);
        let scratch = io
            .model
            .events
            .iter()
            .position(|e| e[..2] == [0, 0x1438])
            .unwrap();
        let health = io
            .model
            .events
            .iter()
            .position(|e| *e == [1, SEC2 + MBOX0, 0xa5a5c3c3])
            .unwrap();
        assert!(scratch < health);
    }
}

#[test]
fn incomplete_or_failed_fwsec_never_prepares_or_starts_booter() {
    for fault in 0..6 {
        let (mut fwsec, loader, booter) = assets();
        let mut io = CompletingFwsec::new(3);
        let expected = match fault {
            0 => {
                io.delay_ticks = u32::MAX;
                Error::Timeout
            }
            1 => {
                io.scratch = 0x10000;
                Error::NotReady
            }
            2 => {
                io.finish_lo ^= 0x100;
                Error::NotReady
            }
            3 => {
                io.finish_hi ^= 0x100;
                Error::NotReady
            }
            4 => {
                io.finish_cpu = u32::MAX;
                Error::Device
            }
            5 => {
                io.scratch = 0xbadf5040;
                Error::Device
            }
            _ => unreachable!(),
        };
        let prepared = std::cell::Cell::new(false);
        let mut tx = Transaction::new();
        assert_eq!(
            tx.run(
                &mut io,
                Control::Zero,
                0x117000000,
                |io| {
                    io.flush();
                    Ok(Fwsec {
                        bytes: &mut fwsec,
                        dma: 0x118000000,
                        loader: &loader,
                    })
                },
                |io| {
                    prepared.set(true);
                    io.flush();
                    Ok(&booter)
                }
            ),
            Err(expected)
        );
        assert_eq!(tx.failed_at(), Some(Phase::FwsecCompletion));
        assert!(!prepared.get());
        assert!(!io.sec_started);
        assert_eq!(io.results.len(), 1);
        assert!(!io.model.events.contains(&[1, SEC2 + MBOX0, 0xa5a5c3c3]));
        if fault == 0 {
            assert_eq!(io.results[0].waited_us, 5_000_000);
            assert!(io.results[0].registers.is_none());
            assert!(!io.model.events.iter().any(|e| e[..2] == [0, 0x1438]));
        }
        let saved = io.model.events.clone();
        assert_eq!(
            tx.run(
                &mut io,
                Control::Zero,
                0x117000000,
                |_| panic!("no retry"),
                |_| panic!("no retry")
            ),
            Err(Error::AlreadyAttempted)
        );
        assert_eq!(saved, io.model.events);
    }
}

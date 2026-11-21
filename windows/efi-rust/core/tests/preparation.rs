// SPDX-License-Identifier: MIT
use std::fmt::Write as _;
use std::io::Write as _;
use std::process::{Command, Stdio};

use cmp40_efi_core::allocation::{self, Allocator, HighSlots, Request};
use cmp40_efi_core::timer::{self, Rtc};
use cmp40_efi_core::transaction::Io;

fn reference(mode: &str, input: &str) -> Vec<u8> {
    let mut child = Command::new(std::env::var("PYTHON").unwrap_or_else(|_| "python3".into()))
        .args([
            concat!(
                env!("CARGO_MANIFEST_DIR"),
                "/tests/c_preparation_reference.py"
            ),
            mode,
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(input.as_bytes())
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    output.stdout
}

#[derive(Default)]
struct PageModel {
    calls: u32,
    mask: u32,
    trace: Vec<[u64; 4]>,
    occupied: Vec<(u64, u64)>,
}

impl PageModel {
    fn overlaps(&self, address: u64, bytes: u64) -> bool {
        self.occupied
            .iter()
            .any(|&(start, size)| address < start + size && start < address + bytes)
    }
}

impl Allocator for PageModel {
    type Error = u32;
    fn reserve(&mut self, pages: usize, request: Request) -> Result<u64, u32> {
        let (kind, input) = match request {
            Request::Address(a) => (2, a),
            Request::MaxAddress(a) => (1, a),
        };
        self.trace.push([0, kind, pages as u64, input]);
        let fail = self.mask & (1 << self.calls) != 0;
        self.calls += 1;
        if fail {
            return Err(7);
        }
        let bytes = pages as u64 * 4096;
        let mut actual = input;
        if kind == 1 {
            actual = 0x70000000 - bytes;
            while self.overlaps(actual, bytes) {
                actual -= bytes;
            }
        }
        if self.overlaps(actual, bytes) {
            return Err(7);
        }
        self.occupied.push((actual, bytes));
        Ok(actual)
    }
}

#[test]
fn high_slots_and_all_fallbacks_match_c_with_occupied_ranges() {
    let mut input = String::new();
    let mut expected = Vec::new();
    // Actual payload, Booter, GspRmBoot, dummy/radix, metadata and FWSEC page counts.
    for pages in [16, 15, 6, 7163, 7179, 1, 10] {
        for first_failures in [0, 1, 11, 12, 13, 14, 15, 16] {
            for second_failures in [0, 1, 11, 12, 13, 14, 15, 16] {
                let first = (1u32 << first_failures) - 1;
                let second = (1u32 << second_failures) - 1;
                writeln!(input, "{pages} {first} {second}").unwrap();
                let mut model = PageModel::default();
                let mut policy = HighSlots::new();
                for mask in [first, second] {
                    model.mask = mask;
                    model.calls = 0;
                    let (status, address) = match policy.reserve(&mut model, pages) {
                        Ok(address) => (0, address),
                        Err(allocation::Error::Backend(code)) => (u64::from(code), 0),
                        Err(other) => panic!("unexpected allocator error: {other:?}"),
                    };
                    model
                        .trace
                        .push([1, status, address, policy.next_address()]);
                }
                for row in model.trace {
                    for value in row {
                        expected.extend_from_slice(&value.to_le_bytes());
                    }
                }
            }
        }
    }
    let actual = reference("allocation", &input);
    assert_eq!(actual.len(), expected.len());
    for (index, (actual, expected)) in actual
        .chunks_exact(32)
        .zip(expected.chunks_exact(32))
        .enumerate()
    {
        assert_eq!(actual, expected, "allocation event {index}");
    }
}

#[test]
fn invalid_allocation_sizes_refuse_before_the_backend() {
    for pages in [0, usize::MAX, usize::MAX / 4096 + 1] {
        let mut model = PageModel::default();
        assert_eq!(
            HighSlots::new().reserve(&mut model, pages),
            Err(allocation::Error::InvalidPages)
        );
        assert!(model.trace.is_empty());
    }
}

#[derive(Default)]
struct Writes(Vec<u8>);
impl Io for Writes {
    fn read32(&mut self, _: u32) -> u32 {
        panic!("PTIMER seed must not read MMIO");
    }
    fn write32(&mut self, offset: u32, value: u32) {
        self.0.extend_from_slice(&offset.to_le_bytes());
        self.0.extend_from_slice(&value.to_le_bytes());
    }
    fn stall(&mut self, _: u32) {
        panic!("PTIMER seed must not add a delay");
    }
    fn flush(&mut self) {
        panic!("PTIMER seed must not add WBINVD");
    }
}

#[test]
fn ptimer_writes_match_c_calendar_arithmetic_and_fallback() {
    let mut input = String::new();
    let mut expected = Writes::default();
    for year in [2014, 2015, 2024, 2026, 2099, 2100] {
        for month in [1, 2, 3, 8, 12] {
            for day in [1, 17, 28] {
                for ok in [false, true] {
                    writeln!(input, "{year} {month} {day} 23 42 19 {}", u8::from(ok)).unwrap();
                    timer::seed(
                        &mut expected,
                        ok.then_some(Rtc {
                            year,
                            month,
                            day,
                            hour: 23,
                            minute: 42,
                            second: 19,
                        }),
                    );
                }
            }
        }
    }
    assert_eq!(reference("timer", &input), expected.0);
    let rtc = Rtc {
        year: 2026,
        month: 11,
        day: 17,
        hour: 0,
        minute: 0,
        second: 0,
    };
    assert_eq!(
        timer::seed_ns(Some(rtc)),
        timer::seed_ns(Some(Rtc { day: 1, ..rtc })),
        "pin the C omission until a separate change"
    );
    for rtc in [
        Rtc { month: 0, ..rtc },
        Rtc { month: 13, ..rtc },
        Rtc { day: 0, ..rtc },
        Rtc { second: 60, ..rtc },
    ] {
        assert_eq!(timer::seed_ns(Some(rtc)), timer::seed_ns(None));
    }
}

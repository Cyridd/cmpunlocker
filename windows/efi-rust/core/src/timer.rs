// SPDX-License-Identifier: MIT
//! Preserve the C PTIMER seed arithmetic during the port. The reference
//! intentionally used here omits the day of the month and the EFI timezone;
//! correcting that behavior is a separate change requiring hardware checks.
use crate::transaction::Io;

#[derive(Clone, Copy, Debug)]
pub struct Rtc {
    pub year: u16,
    pub month: u8,
    pub day: u8,
    pub hour: u8,
    pub minute: u8,
    pub second: u8,
}

const FALLBACK: u64 = 1_787_011_200_000_000_000;

fn leap(year: u16) -> bool {
    year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)
}

pub fn seed_ns(rtc: Option<Rtc>) -> u64 {
    let Some(rtc) = rtc.filter(|t| {
        (2015..2100).contains(&t.year)
            && (1..=12).contains(&t.month)
            && (1..=31).contains(&t.day)
            && t.hour < 24
            && t.minute < 60
            && t.second < 60
    }) else {
        return FALLBACK;
    };
    let mut days: u64 = (1970..rtc.year).map(|y| 365 + u64::from(leap(y))).sum();
    let month_days = [31_u64, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
    days += month_days[..usize::from(rtc.month - 1)].iter().sum::<u64>();
    if rtc.month > 2 && leap(rtc.year) {
        days += 1;
    }
    (days * 86400 + u64::from(rtc.hour) * 3600 + u64::from(rtc.minute) * 60 + u64::from(rtc.second))
        * 1_000_000_000
}

pub fn seed(io: &mut impl Io, rtc: Option<Rtc>) {
    let ns = seed_ns(rtc);
    io.write32(0x9410, (ns >> 32) as u32);
    io.write32(0x9400, ns as u32);
}

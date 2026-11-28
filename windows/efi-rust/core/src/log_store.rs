// SPDX-License-Identifier: MIT
//! Ownership syntax and retention decisions; no filesystem or allocation.
pub const BUDGET: u64 = 8 * 1024 * 1024;
pub const HEADER_TAG: &str = "CMPUNLOCKER_LOG_V3";
pub const FOOTER_TAG: &str = "CMPUNLOCKER_END_V3";
const LEGACY_FOOTER_TAG: &str = "CMP40_END_V3";
const FOOTER_FIELDS_LEN: usize = b" kind=S bytes=00000000000000000000 fnv=0000000000000000\n".len();
pub const FOOTER_LEN: usize = FOOTER_TAG.len() + FOOTER_FIELDS_LEN;
const LEGACY_FOOTER_LEN: usize = LEGACY_FOOTER_TAG.len() + FOOTER_FIELDS_LEN;
pub const FNV_INITIAL: u64 = 0xcbf29ce484222325;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Kind {
    Summary,
    Trace,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error {
    Quota,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Record {
    pub generation: u32,
    pub bytes: u64,
    pub kind: Kind,
    pub complete: bool,
}

pub fn checksum(mut digest: u64, bytes: &[u8]) -> u64 {
    for &byte in bytes {
        digest = (digest ^ u64::from(byte)).wrapping_mul(0x100000001b3);
    }
    digest
}
pub fn filename_generation(name: &str) -> Option<u32> {
    let digits = name.strip_prefix('L')?.strip_suffix(".LOG")?;
    if digits.len() != 7 || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    digits.parse().ok().filter(|&number| number != 0)
}
pub fn owns(header: &[u8], generation: u32) -> bool {
    owned_footer_len(header, generation).is_some()
}
pub fn owned_footer_len(header: &[u8], generation: u32) -> Option<usize> {
    let end = header.iter().position(|&byte| byte == b'\n')?;
    let text = core::str::from_utf8(&header[..end]).ok()?;
    let mut words = text.split_ascii_whitespace();
    let footer_len = match words.next()? {
        HEADER_TAG => FOOTER_LEN,
        "CMP40_LOG_V3" => LEGACY_FOOTER_LEN,
        _ => return None,
    };
    (words
        .next()
        .and_then(|word| word.strip_prefix("generation="))
        .filter(|digits| digits.len() == 7 && digits.bytes().all(|byte| byte.is_ascii_digit()))
        .and_then(|digits| digits.parse::<u32>().ok())
        == Some(generation)
        && matches!(
            words.next(),
            Some("control=full" | "control=zero" | "control=logcheck" | "control=entrycheck")
        )
        && words.next().is_none())
    .then_some(footer_len)
}
pub fn footer(bytes: &[u8], length: u64, digest: u64) -> Option<Kind> {
    if bytes.last() != Some(&b'\n') {
        return None;
    }
    let text = core::str::from_utf8(bytes).ok()?;
    let mut words = text.split_ascii_whitespace();
    match (words.next()?, bytes.len()) {
        (FOOTER_TAG, FOOTER_LEN) | (LEGACY_FOOTER_TAG, LEGACY_FOOTER_LEN) => (),
        _ => return None,
    }
    let kind = match words.next()? {
        "kind=S" => Kind::Summary,
        "kind=T" => Kind::Trace,
        _ => return None,
    };
    let digits = words.next()?.strip_prefix("bytes=")?;
    let hash = words.next()?.strip_prefix("fnv=")?;
    (digits.len() == 20
        && digits.bytes().all(|byte| byte.is_ascii_digit())
        && digits.parse::<u64>().ok() == Some(length)
        && hash.len() == 16
        && hash.bytes().all(|byte| byte.is_ascii_hexdigit())
        && u64::from_str_radix(hash, 16).ok() == Some(digest)
        && words.next().is_none())
    .then_some(kind)
}

/// Keep the most recent verified record until a new verified record exists.
/// Incomplete owned files count as diagnostics. Active bytes include the footer
/// reservation, so temporary/checkpoint data cannot escape the total quota.
pub fn victim(
    records: &[Record],
    active: Option<(Option<Kind>, u64)>,
) -> Result<Option<usize>, Error> {
    let protected = records
        .iter()
        .filter(|record| record.complete)
        .map(|record| record.generation)
        .max();
    let (mut summaries, mut traces, mut total) = (0, 0, 0u64);
    for record in records {
        match record.kind {
            Kind::Summary => summaries += 1,
            Kind::Trace => traces += 1,
        }
        total = total.checked_add(record.bytes).ok_or(Error::Quota)?;
    }
    if let Some((kind, bytes)) = active {
        if bytes > BUDGET {
            return Err(Error::Quota);
        }
        total = total.checked_add(bytes).ok_or(Error::Quota)?;
        match kind {
            Some(Kind::Summary) => summaries += 1,
            Some(Kind::Trace) => traces += 1,
            None => (),
        }
    }
    let excess = if summaries > 32 {
        Some(Kind::Summary)
    } else if traces > 4 {
        Some(Kind::Trace)
    } else {
        None
    };
    if excess.is_none() && total <= BUDGET {
        return Ok(None);
    }
    let eligible = |record: &Record| {
        Some(record.generation) != protected && excess.is_none_or(|kind| record.kind == kind)
    };
    // Under byte pressure reclaim old large diagnostics before small summaries.
    // Count pressure still selects only the class that exceeds its own limit.
    let prefer_trace = excess.is_none()
        && records
            .iter()
            .any(|record| eligible(record) && record.kind == Kind::Trace);
    records
        .iter()
        .enumerate()
        .filter(|(_, record)| eligible(record) && (!prefer_trace || record.kind == Kind::Trace))
        .min_by_key(|(_, record)| record.generation)
        .map(|(index, _)| Some(index))
        .ok_or(Error::Quota)
}

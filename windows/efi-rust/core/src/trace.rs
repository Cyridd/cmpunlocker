// SPDX-License-Identifier: MIT
//! Lossless serialization of retained events. Never used during hardware IO.

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Event {
    pub kind: u8,
    pub offset: u32,
    pub value: u32,
    pub count: u32,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Record {
    Event(Event),
    /// Repeat the preceding R/S event pair, including each event's count.
    RepeatPair {
        additional: usize,
    },
}

/// Emits two events followed by one repeat record for identical R/S pairs.
/// Equality includes counts, addresses, read values and delay lengths.
pub fn encode(events: &[Event]) -> impl Iterator<Item = Record> + '_ {
    let mut index = 0;
    let mut second = None;
    let mut repeat = None;
    core::iter::from_fn(move || {
        if let Some(event) = second.take() {
            return Some(Record::Event(event));
        }
        if let Some(additional) = repeat.take() {
            return Some(Record::RepeatPair { additional });
        }
        let first = *events.get(index)?;
        if first.kind == b'R'
            && events
                .get(index + 1)
                .is_some_and(|event| event.kind == b'S')
        {
            let pair = &events[index..index + 2];
            let mut end = index + 2;
            while events.get(end..end + 2) == Some(pair) {
                end += 2;
            }
            if end > index + 2 {
                second = Some(pair[1]);
                repeat = Some((end - index) / 2 - 1);
                index = end;
                return Some(Record::Event(first));
            }
        }
        index += 1;
        Some(Record::Event(first))
    })
}

/// An error, explicit diagnostic request or incomplete capture requires detail.
pub fn full_log(requested: bool, failed: bool, diagnostic: bool) -> bool {
    requested || failed || diagnostic
}

pub const MAX_EVENTS: usize = 131072;

/// Bound all possible live functions, not just the successful board's trace.
/// ReBAR: one three-read scan plus twelve six-read conflict scans per slot.
/// Gen2: three-read discovery plus generous headroom for capability walks,
/// leaf-bus rechecks, 200 link polls and target restoration. Clamp to the
/// existing capture limit; larger topologies retain its overflow reporting.
pub fn phase_capacity(rebar: bool, roots: usize, target_bus: u8) -> usize {
    let slots = roots.min(8) * usize::from(target_bus) * 32 * 8;
    let bound = if rebar {
        512 + slots * 75
    } else {
        16384 + slots * 3
    };
    bound.min(MAX_EVENTS)
}

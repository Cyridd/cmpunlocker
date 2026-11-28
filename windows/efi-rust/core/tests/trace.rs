// SPDX-License-Identifier: MIT
use cmp40_efi_core::{
    options::UnlockOptions,
    trace::{encode, full_log, Event, Record},
};

fn event(kind: u8, value: u32, count: u32) -> Event {
    Event {
        kind,
        offset: if kind == b'R' { 0x1100f4 } else { 0 },
        value,
        count,
    }
}

fn round_trip(events: &[Event]) -> Vec<Record> {
    let records: Vec<_> = encode(events).collect();
    let mut restored = Vec::new();
    for record in &records {
        match *record {
            Record::Event(event) => restored.push(event),
            Record::RepeatPair { additional } => {
                let pair = [restored[restored.len() - 2], restored[restored.len() - 1]];
                assert_eq!([pair[0].kind, pair[1].kind], [b'R', b'S']);
                assert!(restored.len() + additional * 2 <= events.len());
                for _ in 0..additional {
                    restored.extend(pair);
                }
            }
        }
    }
    assert_eq!(restored, events);
    records
}

#[test]
fn long_poll_is_three_records_and_preserves_final_read() {
    let pair = [event(b'R', 0, 1), event(b'S', 100, 1)];
    let mut events = pair.repeat(10000);
    events.push(event(b'R', 1, 1));
    assert_eq!(
        round_trip(&events),
        vec![
            Record::Event(pair[0]),
            Record::Event(pair[1]),
            Record::RepeatPair { additional: 9999 },
            Record::Event(event(b'R', 1, 1)),
        ]
    );
}

#[test]
fn changing_values_delays_counts_and_intervening_writes_end_runs() {
    let pair = [event(b'R', 0, 1), event(b'S', 100, 1)];
    for changed in [
        event(b'R', 1, 1),
        Event {
            offset: 0x1100f8,
            ..pair[0]
        },
        event(b'S', 101, 1),
        event(b'S', 100, 2),
        event(b'W', 0, 1),
    ] {
        let mut events = pair.repeat(3);
        events.push(changed);
        events.extend(pair.repeat(2));
        let records = round_trip(&events);
        assert!(records.contains(&Record::Event(changed)));
    }
    assert!(round_trip(&[]).is_empty());
    round_trip(&pair[..1]);
    round_trip(&[event(b'R', 0, u32::MAX), event(b'S', 100, u32::MAX)].repeat(2));
    // Config reads, writes and cache barriers are never treated as MMIO polls.
    let other = [event(b'P', 0, 1), event(b'S', 100, 1)].repeat(4);
    assert_eq!(round_trip(&other).len(), other.len());
}

#[test]
fn full_capture_capacity_and_mixed_streams_round_trip() {
    round_trip(&[event(b'R', 0, 1), event(b'S', 100, 1)].repeat(65536));
    let mut seed = 123u32;
    let mut events = Vec::new();
    for _ in 0..1000 {
        seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
        let pair = [event(b'R', seed & 3, 1), event(b'S', 100, 1)];
        events.extend(pair.repeat((seed as usize % 8) + 1));
    }
    round_trip(&events);
}

#[test]
fn normal_success_is_summary_and_every_promotion_is_full() {
    for requested in [false, true] {
        for failed in [false, true] {
            for diagnostic in [false, true] {
                assert_eq!(
                    full_log(requested, failed, diagnostic),
                    requested || failed || diagnostic
                );
            }
        }
    }
}

#[test]
fn trace_option_is_exact_and_independent_of_handoff_and_rebar() {
    for (text, expected) in [
        ("", false),
        ("--trace", true),
        ("--trace norebar", true),
        ("--trace --return-to-limine", true),
        ("--trace-extra", false),
        ("--TRACE", false),
        ("\0--trace", false),
    ] {
        let bytes: Vec<_> = text.encode_utf16().flat_map(u16::to_le_bytes).collect();
        assert_eq!(UnlockOptions::parse(&bytes).unwrap().trace, expected);
    }
}

#[test]
fn phase_capacities_cover_the_conservative_topology_bounds() {
    use cmp40_efi_core::trace::{phase_capacity, MAX_EVENTS};
    for roots in 0..=8 {
        for bus in 0..=255 {
            let slots = roots * usize::from(bus) * 256;
            assert_eq!(
                phase_capacity(true, roots, bus),
                (512 + slots * 75).min(MAX_EVENTS)
            );
            assert_eq!(
                phase_capacity(false, roots, bus),
                (16384 + slots * 3).min(MAX_EVENTS)
            );
        }
    }
    assert!(phase_capacity(false, 1, 16) < MAX_EVENTS);
    assert!(phase_capacity(true, 1, 1) < MAX_EVENTS);
}

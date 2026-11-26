// SPDX-License-Identifier: MIT
use cmp40_efi_core::log_store::{self, Kind, Record, BUDGET, FNV_INITIAL, FOOTER_LEN};

#[test]
fn ownership_and_footer_refuse_foreign_stale_and_corrupted_records() {
    let header = b"CMP40_LOG_V3 generation=0001234 control=full\n";
    assert!(log_store::owns(header, 1234));
    for bad in [
        b"CMP40_LOG_V2 generation=0001234 control=full\n".as_slice(),
        b"CMP40_LOG_V3 generation=0001235 control=full\n",
        b"CMP40_LOG_V3 generation=0001234 control=other\n",
        b"CMP40_LOG_V3",
    ] {
        assert!(!log_store::owns(bad, 1234));
    }
    assert_eq!(log_store::filename_generation("L0001234.LOG"), Some(1234));
    for bad in [
        "RF000001.LOG",
        "L0000000.LOG",
        "L00000001.LOG",
        "L+001234.LOG",
        "KEEP.LOG",
    ] {
        assert_eq!(log_store::filename_generation(bad), None);
    }
    let digest = log_store::checksum(FNV_INITIAL, header);
    let tail = format!(
        "CMP40_END_V3 kind=T bytes={:020} fnv={:016x}\n",
        header.len(),
        digest
    );
    assert_eq!(tail.len(), FOOTER_LEN);
    assert_eq!(
        log_store::footer(tail.as_bytes(), header.len() as u64, digest),
        Some(Kind::Trace)
    );
    assert_eq!(
        log_store::footer(tail.as_bytes(), header.len() as u64 + 1, digest),
        None
    );
    assert_eq!(
        log_store::footer(tail.as_bytes(), header.len() as u64, digest ^ 1),
        None
    );
    assert_eq!(
        log_store::footer(
            &tail.as_bytes()[..FOOTER_LEN - 1],
            header.len() as u64,
            digest
        ),
        None
    );
}

fn prune(records: &mut Vec<Record>, active: Option<(Option<Kind>, u64)>) {
    while let Some(index) = log_store::victim(records, active).unwrap() {
        records.remove(index);
    }
}
#[test]
fn hundreds_of_variable_runs_keep_counts_bytes_and_latest_verified_record() {
    let mut records: Vec<Record> = Vec::new();
    for generation in 1..=500 {
        let previous = records
            .iter()
            .filter(|r| r.complete)
            .map(|r| r.generation)
            .max();
        let kind = if generation % 7 == 0 {
            Kind::Trace
        } else {
            Kind::Summary
        };
        let bytes = if kind == Kind::Trace {
            1_500_000 + (generation as u64 % 4) * 100_000
        } else {
            4000
        };
        prune(&mut records, Some((Some(Kind::Trace), bytes)));
        assert!(previous.is_none_or(|id| records.iter().any(|r| r.generation == id)));
        assert!(records.iter().map(|r| r.bytes).sum::<u64>() + bytes <= BUDGET);
        records.push(Record {
            generation,
            bytes,
            kind: if generation % 17 == 0 {
                Kind::Trace
            } else {
                kind
            },
            complete: generation % 17 != 0,
        });
        prune(&mut records, None);
        assert!(records.iter().filter(|r| r.kind == Kind::Summary).count() <= 32);
        assert!(records.iter().filter(|r| r.kind == Kind::Trace).count() <= 4);
        assert!(records.iter().map(|r| r.bytes).sum::<u64>() <= BUDGET);
    }
}
#[test]
fn quota_cannot_reclaim_last_verified_record_or_overflow_on_huge_sizes() {
    let records = [Record {
        generation: 1,
        bytes: BUDGET - 100,
        kind: Kind::Trace,
        complete: true,
    }];
    assert!(log_store::victim(&records, Some((Some(Kind::Summary), 101))).is_err());
    assert!(log_store::victim(&records, Some((Some(Kind::Trace), BUDGET + 1))).is_err());
    let enormous = [
        Record {
            bytes: u64::MAX,
            ..records[0]
        },
        records[0],
    ];
    assert!(log_store::victim(&enormous, None).is_err());
    let mut many = vec![
        Record {
            generation: 2,
            ..records[0]
        },
        Record {
            generation: 3,
            ..records[0]
        },
    ];
    let victim = log_store::victim(&many, None).unwrap().unwrap();
    assert_eq!(many[victim].generation, 2);
    // A failed deletion leaves the candidate in the inventory and cannot
    // claim that its bytes were reclaimed.
    assert_eq!(log_store::victim(&many, None).unwrap(), Some(victim));
    many.remove(victim);
    assert_eq!(log_store::victim(&many, None).unwrap(), None);
}

// SPDX-License-Identifier: MIT
//! Differential tests against actual functions extracted from the project's C EFI.
use std::fmt::Write as _;
use std::io::Write as _;
use std::process::{Command, Stdio};

use cmp40_efi_core::metadata::{DmaAddresses, SignatureKind, WprMetadata, IMAGE_BYTES};
use cmp40_efi_core::options::UnlockOptions;
use cmp40_efi_core::radix::RadixPlan;
use cmp40_efi_core::state::{Control, Snapshot, StateError, STOCK_PLM, WPR2_LO_DOWN};

fn reference(mode: &str, input: &str) -> Vec<u8> {
    let script = concat!(env!("CARGO_MANIFEST_DIR"), "/tests/c_reference.py");
    let python = std::env::var("PYTHON").unwrap_or_else(|_| "python3".into());
    let mut child = Command::new(python)
        .args([script, mode])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("C parity tests require Python 3 and a host C compiler (CC, default gcc)");
    child
        .stdin
        .take()
        .unwrap()
        .write_all(input.as_bytes())
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(
        output.status.success(),
        "C reference failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    output.stdout
}

#[test]
fn metadata_matches_all_256_c_bytes_in_both_signature_modes() {
    let cases = [
        DmaAddresses {
            radix: 0x100000,
            signature: 0x200000,
            bootloader: 0x300000,
        },
        DmaAddresses {
            radix: 0x110000000,
            signature: 0x112003000,
            bootloader: 0x114005000,
        },
        DmaAddresses {
            radix: 0x1a0010000,
            signature: 0x1c0020000,
            bootloader: 0x1e0030000,
        },
        // Optimized sequence if every initial high-slot request succeeds.
        DmaAddresses {
            radix: 0x113025000,
            signature: 0x110000000,
            bootloader: 0x11201f000,
        },
    ];
    let mut input = String::new();
    let mut expected = Vec::new();
    for addresses in cases {
        for signature in [SignatureKind::Payload, SignatureKind::Stock] {
            writeln!(
                input,
                "{} {} {} {}",
                addresses.radix,
                addresses.signature,
                addresses.bootloader,
                u8::from(signature == SignatureKind::Stock)
            )
            .unwrap();
            expected.extend_from_slice(
                WprMetadata::small_wpr(addresses, signature)
                    .unwrap()
                    .as_bytes(),
            );
        }
    }
    assert_eq!(reference("metadata", &input), expected);
}

#[test]
fn radix_matches_c_across_leaf_boundaries_and_the_real_image_size() {
    compare_radix(false);
}

#[test]
fn in_place_zero_image_matches_c_across_leaf_boundaries_and_the_real_image_size() {
    compare_radix(true);
}

fn compare_radix(zero_image: bool) {
    let sizes = [
        1,
        4096,
        4097,
        511 * 4096 + 37,
        512 * 4096,
        513 * 4096,
        IMAGE_BYTES,
    ];
    let mut input = String::new();
    for (index, size) in sizes.into_iter().enumerate() {
        writeln!(
            input,
            "{size} {} {}",
            0x110000000_u64 + index as u64 * 0x4000000,
            u8::from(zero_image)
        )
        .unwrap();
    }
    let output = reference("radix", &input);
    let mut offset = 0;
    for (index, size) in sizes.into_iter().enumerate() {
        let plan = RadixPlan::new(size).unwrap();
        let mut buffer = vec![0xa5; plan.total_bytes() + 16];
        let dma_base = 0x110000000 + index as u64 * 0x4000000;
        if zero_image {
            plan.write_zero_image(&mut buffer, dma_base).unwrap();
        } else {
            let image: Vec<u8> = (0..size).map(|i| (i % 251) as u8).collect();
            plan.write_image(&mut buffer, dma_base, &image).unwrap();
        }
        let bytes = plan.table_bytes() + size;
        assert_eq!(
            &output[offset..offset + bytes],
            &buffer[..bytes],
            "image size {size}"
        );
        assert!(
            buffer[bytes..plan.total_bytes()].iter().all(|&b| b == 0),
            "last-leaf padding"
        );
        assert!(
            buffer[plan.total_bytes()..].iter().all(|&b| b == 0xa5),
            "caller buffer tail"
        );
        offset += bytes;
    }
    assert_eq!(offset, output.len());
}

fn hex(bytes: &[u8]) -> String {
    if bytes.is_empty() {
        return "-".into();
    }
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn utf16(text: &str) -> Vec<u8> {
    text.encode_utf16().flat_map(u16::to_le_bytes).collect()
}

#[test]
fn options_match_c_including_binary_optional_data() {
    let mut options: Vec<Vec<u8>> = [
        "",
        "40HXUNLK.EFI",
        "--return-to-bootloader",
        "--return-to-grub",
        "--return-to-limine",
        "\t40HXUNLK.EFI\r\n--return-to-grub\t",
        "--return-to-grub-other",
        "prefix--return-to-grub",
        "--RETURN-TO-GRUB",
        "--handoff-preload",
        "--handoff-stock",
        "--handoff-flr",
        "--handoff-gsp-reset",
        "--handoff-stock --handoff-preload",
        "--handoff-stock --return-to-grub",
        "\0--return-to-grub",
        "--return-to-grub\0--handoff-stock",
        "\u{2003}--return-to-grub",
    ]
    .into_iter()
    .map(utf16)
    .collect();
    options.push(b"WINDOWS\0\x01\x00\xff\x00".to_vec());
    options.push(vec![0xff]);
    let mut odd = utf16("--return-to-grub");
    odd.push(0xff);
    options.push(odd);
    let mut input = String::new();
    for bytes in &options {
        writeln!(input, "{}", hex(bytes)).unwrap();
    }
    let output = reference("options", &input);
    assert_eq!(output.len(), options.len() * 8);
    for (bytes, row) in options.iter().zip(output.chunks_exact(8)) {
        let valid = i32::from_le_bytes(row[..4].try_into().unwrap()) != 0;
        let parent = i32::from_le_bytes(row[4..].try_into().unwrap()) != 0;
        let result = UnlockOptions::parse(bytes);
        assert_eq!(result.is_ok(), valid, "load options {bytes:?}");
        if let Ok(result) = result {
            assert_eq!(result.return_to_bootloader, parent);
        }
    }
}

fn status(result: Result<(), StateError>) -> u32 {
    match result {
        Ok(()) => 0,
        Err(StateError::NotReady) => 6,
        Err(StateError::Unreadable) => 7,
    }
}

#[test]
fn entry_and_native_completion_match_c_under_register_faults() {
    let mut rows = Vec::new();
    for (ss0, ss1) in [
        (0, 0),
        (0x40020000, 7),
        (0x00400016, 7),
        (0x88888888, 8),
        (0x88888888, 0),
        (0, 8),
    ] {
        rows.push([ss0, ss1, WPR2_LO_DOWN, 0, 0x10, 0, STOCK_PLM]);
    }
    // A single wrong/unreadable value must not accidentally prove completion.
    for baseline in [rows[0], rows[3]] {
        for field in 0..7 {
            for value in [
                0, 1, 0x10, 8, 0xffffffff, 0xbadf5040, 0xbadf1100, 0xbadf2003, 0x01ffe000,
                0x01ffee00, 0xffffff8f, 0xffffffff,
            ] {
                let mut row = baseline;
                row[field] = value;
                rows.push(row);
            }
        }
    }
    let mut input = String::new();
    for row in &rows {
        writeln!(
            input,
            "{} {} {} {} {} {} {}",
            row[0], row[1], row[2], row[3], row[4], row[5], row[6]
        )
        .unwrap();
    }
    for (mode, control) in [("state-full", Control::Full), ("state-zero", Control::Zero)] {
        let output = reference(mode, &input);
        assert_eq!(output.len(), rows.len() * 8);
        for (row, result) in rows.iter().zip(output.chunks_exact(8)) {
            let snapshot = Snapshot {
                ss0: row[0],
                ss1: row[1],
                wpr2_lo: row[2],
                wpr2_hi: row[3],
                sec2_cpu: row[4],
                mailbox0: row[5],
                feat_plm: row[6],
            };
            assert_eq!(
                status(snapshot.check_entry()),
                u32::from_le_bytes(result[..4].try_into().unwrap()),
                "entry {row:x?}"
            );
            assert_eq!(
                status(snapshot.check_booter_completion(control)),
                u32::from_le_bytes(result[4..].try_into().unwrap()),
                "{control:?} completion {row:x?}"
            );
        }
    }
}

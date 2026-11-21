// SPDX-License-Identifier: MIT
use cmp40_efi_core::metadata::{DmaAddresses, MetadataError, SignatureKind, WprMetadata};
use cmp40_efi_core::radix::{RadixError, RadixPlan};

#[test]
fn cpu_rm_geometry_keeps_the_measured_small_wpr_boundary() {
    let meta = WprMetadata::cpu_rm(
        DmaAddresses {
            radix: 0x110000000,
            signature: 0x112000000,
            bootloader: 0x114000000,
        },
        SignatureKind::Payload,
    )
    .unwrap();
    for (offset, expected) in [
        (0x58, 0x1fdf00000),
        (0x60, 0x1fdf00000),
        (0x70, 0x1fe000000),
        (0x78, 0x1fe100000),
        (0x80, 0xf0000),
        (0x88, 0x1fe1f0000),
        (0xa8, 0x1fff00000),
        (0xb0, 0x200000000),
    ] {
        assert_eq!(
            u64::from_le_bytes(meta.as_bytes()[offset..offset + 8].try_into().unwrap()),
            expected
        );
    }
    assert_eq!(meta.as_bytes()[0xf1], 1);
    assert!(meta.as_bytes()[0xc8..0xf1].iter().all(|&byte| byte == 0));
    assert!(meta.as_bytes()[0xf2..].iter().all(|&byte| byte == 0));
}

#[test]
fn metadata_rejects_bad_dma_addresses_before_serialization() {
    for base in [0, 1, 0x110000001, u64::MAX, 1 << 49, (1 << 49) - 4096] {
        let valid = DmaAddresses {
            radix: 0x110000000,
            signature: 0x112000000,
            bootloader: 0x114000000,
        };
        for addresses in [
            DmaAddresses {
                signature: base,
                ..valid
            },
            DmaAddresses {
                bootloader: base,
                ..valid
            },
        ] {
            assert_eq!(
                WprMetadata::cpu_rm(addresses, SignatureKind::Payload),
                Err(MetadataError::InvalidDmaAddress)
            );
        }
    }
}

#[test]
fn radix_refuses_unsupported_sizes_without_integer_wrap() {
    assert_eq!(RadixPlan::new(0), Err(RadixError::EmptyImage));
    assert_eq!(
        RadixPlan::new(512 * 512 * 4096 + 1),
        Err(RadixError::ImageTooLarge)
    );
    assert_eq!(RadixPlan::new(usize::MAX), Err(RadixError::ImageTooLarge));
    assert!(RadixPlan::new(512 * 512 * 4096).is_ok());
}

#[test]
fn invalid_radix_inputs_leave_the_caller_buffer_untouched() {
    let plan = RadixPlan::new(4096).unwrap();
    let image = vec![0; 4096];
    let mut buffer = vec![0xa5; plan.total_bytes()];
    for address in [0, 1, 0x110000001, u64::MAX, (1 << 49) - 4096] {
        assert_eq!(
            plan.write_image(&mut buffer, address, &image),
            Err(RadixError::InvalidDmaAddress)
        );
        assert!(buffer.iter().all(|&byte| byte == 0xa5));
        assert_eq!(
            plan.write_zero_image(&mut buffer, address),
            Err(RadixError::InvalidDmaAddress)
        );
        assert!(buffer.iter().all(|&byte| byte == 0xa5));
    }
    assert_eq!(
        plan.write_image(&mut buffer[..4096], 0x110000000, &image),
        Err(RadixError::BufferTooSmall)
    );
    assert_eq!(
        plan.write_zero_image(&mut buffer[..4096], 0x110000000),
        Err(RadixError::BufferTooSmall)
    );
    assert_eq!(
        plan.write_image(&mut buffer, 0x110000000, &image[..4095]),
        Err(RadixError::ImageSizeMismatch)
    );
    assert!(buffer.iter().all(|&byte| byte == 0xa5));
}

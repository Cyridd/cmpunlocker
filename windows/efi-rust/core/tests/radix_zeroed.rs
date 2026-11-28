// SPDX-License-Identifier: MIT
use cmp40_efi_core::radix::RadixPlan;
#[test]
fn zeroed_dma_tables_match_full_builder_and_preserve_existing_leaves() {
    for bytes in [1, 4096, 2097153, 29339080] {
        let plan = RadixPlan::new(bytes).unwrap();
        let mut old = vec![0; plan.total_bytes() + 32];
        let mut new = old.clone();
        plan.write_zero_image(&mut old, 0x110000000).unwrap();
        plan.write_tables(&mut new, 0x110000000).unwrap();
        assert_eq!(old, new);
        new[plan.table_bytes()..].fill(0xa5);
        plan.write_tables(&mut new, 0x110000000).unwrap();
        assert!(new[plan.table_bytes()..].iter().all(|&byte| byte == 0xa5));
        let saved = new.clone();
        assert!(plan.write_tables(&mut new, 1).is_err());
        assert_eq!(new, saved);
    }
}

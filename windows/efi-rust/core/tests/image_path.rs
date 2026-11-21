// SPDX-License-Identifier: MIT
use cmp40_efi_core::image_path::file_path_matches;

const WINDOWS: &str = "\\EFI\\Microsoft\\Boot\\bootmgfw.efi";

fn file_node(text: &str) -> Vec<u8> {
    let data: Vec<u8> = text
        .encode_utf16()
        .chain([0])
        .flat_map(u16::to_le_bytes)
        .collect();
    let mut node = vec![4, 4];
    node.extend_from_slice(&((data.len() + 4) as u16).to_le_bytes());
    node.extend(data);
    node
}

#[test]
fn recognizes_self_at_windows_path_including_split_and_case_variants() {
    for parts in [
        vec![WINDOWS.to_string()],
        vec![WINDOWS.to_ascii_uppercase()],
        vec!["\\EFI\\Microsoft\\".into(), "Boot\\bootmgfw.efi".into()],
    ] {
        let mut path = vec![1, 1, 6, 0, 0, 1]; // non-file node prefix
        for part in parts {
            path.extend(file_node(&part));
        }
        path.extend([0x7f, 0xff, 4, 0]);
        assert!(file_path_matches(&path, WINDOWS));
        assert!(!file_path_matches(&path, "\\EFI\\BOOT\\BOOTX64.EFI"));
    }
}

#[test]
fn rejects_malformed_paths_and_extra_hidden_filename_data() {
    let mut valid = file_node(WINDOWS);
    valid.extend([0x7f, 0xff, 4, 0]);
    for end in 0..valid.len() {
        assert!(!file_path_matches(&valid[..end], WINDOWS));
    }
    let mut bad_length = valid.clone();
    bad_length[2..4].copy_from_slice(&[0, 0]);
    assert!(!file_path_matches(&bad_length, WINDOWS));
    let mut extra = valid.clone();
    extra.push(0);
    assert!(!file_path_matches(&extra, WINDOWS));
    let mut multi = valid.clone();
    let last = multi.len() - 3;
    multi[last] = 1;
    assert!(!file_path_matches(&multi, WINDOWS));
    let mut hidden = file_node(&format!("{WINDOWS}\0extra"));
    hidden.extend([0x7f, 0xff, 4, 0]);
    assert!(!file_path_matches(&hidden, WINDOWS));
}

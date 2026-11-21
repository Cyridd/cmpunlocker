// SPDX-License-Identifier: MIT
//! Bounded comparison of a loaded image's file path for recursion avoidance.

/// Compare concatenated file-path nodes with a fixed ASCII EFI path, using
/// FAT's ASCII case-insensitivity. Malformed or multi-instance paths fail.
pub fn file_path_matches(path: &[u8], expected: &str) -> bool {
    if !expected.is_ascii() || expected.is_empty() {
        return false;
    }
    let expected = expected.as_bytes();
    let mut offset = 0;
    let mut matched = 0;
    let mut saw_file = false;
    while let Some(header) = path.get(offset..offset + 4) {
        let length = u16::from_le_bytes([header[2], header[3]]) as usize;
        if length < 4 {
            return false;
        }
        let Some(node) = path.get(offset..offset + length) else {
            return false;
        };
        if header[0] == 0x7f {
            return header[1] == 0xff
                && length == 4
                && offset + length == path.len()
                && saw_file
                && matched == expected.len();
        }
        if header[..2] == [4, 4] {
            let text = &node[4..];
            if text.len() < 2 || text.len() % 2 != 0 || text[text.len() - 2..] != [0, 0] {
                return false;
            }
            for bytes in text[..text.len() - 2].chunks_exact(2) {
                let value = u16::from_le_bytes([bytes[0], bytes[1]]);
                if value == 0
                    || value > 127
                    || matched >= expected.len()
                    || !(value as u8).eq_ignore_ascii_case(&expected[matched])
                {
                    return false;
                }
                matched += 1;
            }
            saw_file = true;
        }
        offset += length;
    }
    false
}

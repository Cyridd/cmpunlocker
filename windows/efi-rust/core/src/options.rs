// SPDX-License-Identifier: MIT
//! Interpret the existing EFI's bounded UTF-16LE load options.
//! Binary Windows OptionalData is not treated as an ASCII command line.

/// Match a whole, case-sensitive whitespace-delimited token, stopping at
/// the first UTF-16 NUL. An odd trailing byte is ignored like the C EFI.
pub fn has_load_option(bytes: &[u8], option: &str) -> bool {
    let expected_units = option.encode_utf16().count();
    if expected_units == 0 {
        return false;
    }
    let units = bytes.len() / 2;
    let mut index = 0;
    while index < units && unit(bytes, index) != 0 {
        while index < units && whitespace(unit(bytes, index)) {
            index += 1;
        }
        let start = index;
        while index < units && unit(bytes, index) != 0 && !whitespace(unit(bytes, index)) {
            index += 1;
        }
        if index - start == expected_units
            && option
                .encode_utf16()
                .enumerate()
                .all(|(offset, expected)| unit(bytes, start + offset) == expected)
        {
            return true;
        }
    }
    false
}

fn unit(bytes: &[u8], index: usize) -> u16 {
    u16::from_le_bytes([bytes[index * 2], bytes[index * 2 + 1]])
}

fn whitespace(value: u16) -> bool {
    matches!(value, 0x20 | 0x09 | 0x0a | 0x0d)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum OptionsError {
    IncompatibleHandoff,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct UnlockOptions {
    pub return_to_bootloader: bool,
    pub trace: bool,
}

impl UnlockOptions {
    /// Preserve the production C EFI FULL/ZERO stage-control contract:
    /// preload handoff and optional parent return.
    pub fn parse(bytes: &[u8]) -> Result<Self, OptionsError> {
        let has = |option| has_load_option(bytes, option);
        if ["--handoff-stock", "--handoff-flr", "--handoff-gsp-reset"]
            .into_iter()
            .any(has)
        {
            return Err(OptionsError::IncompatibleHandoff);
        }
        Ok(Self {
            trace: has("--trace"),
            return_to_bootloader: [
                "--return-to-bootloader",
                "--return-to-grub",
                "--return-to-limine",
            ]
            .into_iter()
            .any(has),
        })
    }
}

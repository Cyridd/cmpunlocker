// SPDX-License-Identifier: MIT
//! Existing NVIDIA inputs are verified locally and embedded only in the
//! opt-in CPU-RM build. No new firmware image is stored in the Rust source.
use cmp40_efi_core::state::Control;

pub const FULL: &[u8; 0xfa00] = include_bytes!(concat!(env!("OUT_DIR"), "/v67_payload.bin"));
pub const ZERO: &[u8; 0xfa00] = include_bytes!(concat!(env!("OUT_DIR"), "/payload_zero.bin"));
pub const BOOTER: &[u8; 0xe700] =
    include_bytes!(concat!(env!("OUT_DIR"), "/booter_ucode_prod.bin"));
pub const BOOTER_SIG: &[u8; 16] = include_bytes!(concat!(env!("OUT_DIR"), "/booter_sig.bin"));
pub const BOOT: &[u8; 0x6000] = include_bytes!(concat!(env!("OUT_DIR"), "/boot_runtime.bin"));
pub const FWSEC: &[u8; 0x9df0] = include_bytes!(concat!(env!("OUT_DIR"), "/fwsec_40hx_prod.bin"));
pub const LOADER: &[u8; 0x300] = include_bytes!(concat!(env!("OUT_DIR"), "/bl_gsp_tu102.bin"));
pub const MANIFEST: &str = include_str!(concat!(env!("OUT_DIR"), "/assets.json"));
pub fn payload(control: Control) -> &'static [u8] {
    match control {
        Control::Full => FULL,
        Control::Zero => ZERO,
    }
}

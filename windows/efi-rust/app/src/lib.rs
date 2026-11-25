// SPDX-License-Identifier: MIT
//! EFI boot helpers. Firmware operations are compiled only with cpu-rm.
#![no_std]

extern crate alloc;

#[cfg(feature = "cpu-rm")]
pub mod assets;
#[cfg(feature = "cpu-rm")]
pub mod cpu_rm;
#[cfg(feature = "cpu-rm")]
pub mod dma;
#[cfg(feature = "gen2-experiment")]
pub mod gen2;
pub mod handoff;
pub mod hardware;
pub mod image;
#[cfg(feature = "cpu-rm")]
pub mod output;
pub mod policy;
pub mod raw;
#[cfg(feature = "cpu-rm")]
pub mod rebar;
#[cfg(feature = "cpu-rm")]
pub mod terminal;

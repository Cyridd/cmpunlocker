// SPDX-License-Identifier: MIT
//! EFI boot helpers. Firmware operations are compiled only with unlock.
#![no_std]

extern crate alloc;

#[cfg(feature = "unlock")]
pub mod assets;
#[cfg(feature = "unlock")]
pub mod dma;
#[cfg(feature = "gen2-experiment")]
pub mod gen2;
pub mod handoff;
pub mod hardware;
pub mod image;
#[cfg(feature = "unlock")]
mod log_store;
#[cfg(feature = "unlock")]
pub mod output;
pub mod policy;
pub mod raw;
#[cfg(feature = "unlock")]
pub mod rebar;
#[cfg(feature = "unlock")]
pub mod terminal;
#[cfg(feature = "unlock")]
pub mod unlock;

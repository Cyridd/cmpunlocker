// SPDX-License-Identifier: MIT
//! Verdicts over recorded register values for the existing compute unlock path.
//! A verdict performs no reset, cleanup, polling or implicit retry.

pub const SS0_FULL: u32 = 0x8888_8888;
pub const SS1_FULL: u32 = 8;
pub const STOCK_PLM: u32 = 0xffff_ff8f;
pub const WPR2_LO_DOWN: u32 = 0x1fff_fe00;
pub const WPR2_LO_UP: u32 = 0x01ff_e000;
pub const WPR2_HI_UP: u32 = 0x01ff_ee00;
const HALTED: u32 = 0x10;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Control {
    Full,
    Zero,
}

#[derive(Clone, Copy, Debug, Default)]
pub struct Snapshot {
    pub ss0: u32,
    pub ss1: u32,
    pub wpr2_lo: u32,
    pub wpr2_hi: u32,
    pub sec2_cpu: u32,
    pub mailbox0: u32,
    pub feat_plm: u32,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum StateError {
    Unreadable,
    NotReady,
}

fn unavailable(value: u32) -> bool {
    value == u32::MAX || value & 0xffff_0000 == 0xbadf_0000
}

impl Snapshot {
    /// Nonzero locked selectors are allowed, including Linux -> Windows
    /// entry. Only the exact full pair is treated as already unlocked.
    pub fn check_entry(self) -> Result<(), StateError> {
        if [self.ss0, self.ss1, self.wpr2_lo, self.wpr2_hi]
            .into_iter()
            .any(unavailable)
        {
            return Err(StateError::Unreadable);
        }
        if (self.ss0 == SS0_FULL && self.ss1 == SS1_FULL)
            || self.wpr2_hi != 0
            || self.wpr2_lo != WPR2_LO_DOWN
        {
            return Err(StateError::NotReady);
        }
        Ok(())
    }

    /// Check the Booter portion of the production FULL/ZERO completion.
    /// GSP HALT/idle checks and overall handoff sequencing belong to the
    /// caller. Mailbox 1 can retain the high metadata address and is not used.
    pub fn check_booter_completion(self, control: Control) -> Result<(), StateError> {
        if [
            self.ss0,
            self.ss1,
            self.wpr2_lo,
            self.wpr2_hi,
            self.sec2_cpu,
            self.mailbox0,
        ]
        .into_iter()
        .any(unavailable)
        {
            return Err(StateError::Unreadable);
        }
        let (ss0, ss1) = match control {
            Control::Full => (SS0_FULL, SS1_FULL),
            Control::Zero => (0, 0),
        };
        if self.ss0 != ss0
            || self.ss1 != ss1
            || self.wpr2_hi != 0
            || self.wpr2_lo != WPR2_LO_DOWN
            || self.sec2_cpu & HALTED == 0
            || self.mailbox0 != 0
            || self.feat_plm != STOCK_PLM
        {
            return Err(StateError::NotReady);
        }
        Ok(())
    }
}

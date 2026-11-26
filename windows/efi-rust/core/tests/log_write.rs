// SPDX-License-Identifier: MIT
use cmp40_efi_core::log_write::{Error, Sink, State};
struct Model {
    calls: usize,
    position: u64,
    short: bool,
    write_error: bool,
    read_error: bool,
}
impl Sink for Model {
    type Error = u32;
    fn write(&mut self, bytes: &[u8]) -> Result<(), u32> {
        self.calls += 1;
        self.position += if self.short { 1 } else { bytes.len() as u64 };
        if self.write_error {
            Err(28)
        } else {
            Ok(())
        }
    }
    fn position(&mut self) -> Result<u64, u32> {
        if self.read_error {
            Err(5)
        } else {
            Ok(self.position)
        }
    }
}
#[test]
fn success_and_every_partial_failure_preserve_position_and_never_replay() {
    for (short, write_error, read_error, expected) in [
        (false, false, false, None),
        (true, false, false, Some(Error::ShortWrite)),
        (false, true, false, Some(Error::Io(28))),
        (false, false, true, Some(Error::Io(5))),
    ] {
        let mut state = State::new();
        let mut model = Model {
            calls: 0,
            position: 0,
            short,
            write_error,
            read_error,
        };
        assert_eq!(state.commit(&mut model, &[1; 4096]).err(), expected);
        assert_eq!(state.failure(), expected);
        if let Some(error) = expected {
            assert_eq!(state.commit(&mut model, &[2; 4096]), Err(error));
            assert_eq!(model.calls, 1);
            assert_eq!(state.batches(), 0);
        } else {
            state.commit(&mut model, &[2; 100]).unwrap();
            assert_eq!(model.position, 4196);
            assert_eq!(state.batches(), 2);
        }
    }
}
#[test]
fn failed_verification_poison_is_sticky_before_another_write() {
    let mut state = State::new();
    state.poison(Error::Io(5));
    state.poison(Error::ShortWrite);
    let mut sink = Model {
        calls: 0,
        position: 0,
        short: false,
        write_error: false,
        read_error: false,
    };
    assert_eq!(state.commit(&mut sink, &[0; 10]), Err(Error::Io(5)));
    assert_eq!(sink.calls, 0);
}

// SPDX-License-Identifier: MIT
//! Sticky write failures prevent replaying partially appended batches.
pub trait Sink {
    type Error: Copy;
    fn write(&mut self, bytes: &[u8]) -> Result<(), Self::Error>;
    fn position(&mut self) -> Result<u64, Self::Error>;
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error<E> {
    Io(E),
    ShortWrite,
    Overflow,
}
pub struct State<E: Copy> {
    written: u64,
    batches: u64,
    failure: Option<Error<E>>,
}
impl<E: Copy> Default for State<E> {
    fn default() -> Self {
        Self::new()
    }
}
impl<E: Copy> State<E> {
    pub const fn new() -> Self {
        Self {
            written: 0,
            batches: 0,
            failure: None,
        }
    }
    pub fn failure(&self) -> Option<Error<E>> {
        self.failure
    }
    pub fn batches(&self) -> u64 {
        self.batches
    }
    pub fn poison(&mut self, error: Error<E>) {
        if self.failure.is_none() {
            self.failure = Some(error);
        }
    }
    pub fn commit(
        &mut self,
        sink: &mut impl Sink<Error = E>,
        bytes: &[u8],
    ) -> Result<(), Error<E>> {
        if let Some(error) = self.failure {
            return Err(error);
        }
        let result = (|| {
            let expected = self
                .written
                .checked_add(bytes.len() as u64)
                .ok_or(Error::Overflow)?;
            sink.write(bytes).map_err(Error::Io)?;
            if sink.position().map_err(Error::Io)? != expected {
                return Err(Error::ShortWrite);
            }
            self.written = expected;
            self.batches += 1;
            Ok(())
        })();
        if let Err(error) = result {
            self.poison(error);
        }
        result
    }
}

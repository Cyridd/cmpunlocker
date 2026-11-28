# Old C EFI implementation

The Windows installer on this branch uses the [Rust EFI](../../efi-rust/README.md).
The Rust build still reads firmware inputs from this directory, and its tests
compile selected C functions to check that both implementations agree.

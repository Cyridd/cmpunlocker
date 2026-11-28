#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
sysroot=$(rustc --print sysroot)

# Remap local paths and omit the timestamp and CodeView record from release EFI.
flags=$(printf '\x1f%s' \
    "--remap-path-prefix=$PWD=." \
    "--remap-path-prefix=${CARGO_HOME:-$HOME/.cargo}=registry" \
    "--remap-path-prefix=$sysroot=rust-sysroot" \
    '-Clink-arg=/timestamp:0' \
    '-Clink-arg=/debug:none')
export CARGO_ENCODED_RUSTFLAGS=${flags:1}

cargo build --locked --offline --release --target x86_64-unknown-uefi \
    -p cmp40-efi --bin cmp40-unlock-full --features unlock

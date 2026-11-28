// SPDX-License-Identifier: MIT
use std::{env, path::PathBuf, process::Command};

fn main() {
    println!("cargo:rerun-if-changed=prepare_assets.py");
    if env::var_os("CARGO_FEATURE_UNLOCK").is_none() {
        return;
    }
    let app = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap());
    let inputs = app.join("../../tools/unlock40x");
    for name in [
        "v67_payload.bin",
        "booter_ucode_prod.bin",
        "gsp_rm_boot_dbg.bin",
        "fwsec_40hx_prod.bin",
        "bl_gsp_tu102.bin",
    ] {
        println!("cargo:rerun-if-changed={}", inputs.join(name).display());
    }
    println!("cargo:rerun-if-env-changed=PYTHON");
    let status = Command::new(env::var("PYTHON").unwrap_or_else(|_| "python3".into()))
        .arg(app.join("prepare_assets.py"))
        .arg("--inputs")
        .arg(inputs)
        .arg("--output")
        .arg(env::var_os("OUT_DIR").unwrap())
        .status()
        .expect("compute unlock firmware preparation requires Python 3");
    assert!(
        status.success(),
        "Pinned compute unlock input verification failed; EFI build refused"
    );
}

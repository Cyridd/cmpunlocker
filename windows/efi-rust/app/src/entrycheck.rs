// SPDX-License-Identifier: MIT
//! Early-carrier diagnostic. Loads/unloads candidates, never starts them.
#![no_std]
#![no_main]

use cmp40_efi::{
    handoff,
    hardware::Pci,
    image::{load_from_buffer, load_from_volume, protocol},
    output::Output,
    policy,
    raw::Source,
};
use uefi::{boot, cstr16, prelude::*, proto::loaded_image::LoadedImage, CStr16};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    uefi::println!("CMPUNLOCKER-ENTRYCHECK: ENTERED GPU_WRITES=0 CHILD_STARTS=0");
    if let Err(error) = boot::set_watchdog_timer(0, 0x10000, None) {
        uefi::println!(
            "CMPUNLOCKER-ENTRYCHECK: WATCHDOG_FAILED {:?}",
            error.status()
        );
        return error.status();
    }
    // Make even a filesystem-open failure visible to a phone recording.
    boot::stall(core::time::Duration::from_secs(5));
    let status = run().err().unwrap_or(Status::SUCCESS);
    uefi::println!(
        "CMPUNLOCKER-ENTRYCHECK: FINISHED {:?} GPU_WRITES=0 CHILD_STARTS=0; return in 10s",
        status
    );
    boot::stall(core::time::Duration::from_secs(10));
    status
}

fn run() -> Result<(), Status> {
    // Open/verify a log before policy, discovery or candidate image loading.
    let mut output = Output::open("entrycheck").inspect_err(|status| {
        uefi::println!("CMPUNLOCKER-ENTRYCHECK: LOG_OPEN_FAILED {:?}", status);
    })?;
    let result = inspect(&mut output);
    if let Err(status) = result {
        let _ = output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: FAILED {:?}\n",
            status
        ));
    }
    output.seal()?;
    result
}

fn inspect(output: &mut Output) -> Result<(), Status> {
    output.checkpoint(format_args!(
        "CMPUNLOCKER-ENTRYCHECK: BEGIN GPU_WRITES=0 CHILD_STARTS=0\n"
    ))?;
    output.checkpoint(format_args!("CMPUNLOCKER-ENTRYCHECK: POLICY_BEGIN\n"))?;
    match policy::options() {
        Ok(options) => output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: POLICY_OK parent={}\n",
            options.return_to_bootloader
        ))?,
        Err(status) => output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: POLICY_REFUSED {:?}\n",
            status
        ))?,
    }
    output.checkpoint(format_args!("CMPUNLOCKER-ENTRYCHECK: PCI_BEGIN\n"))?;
    match Pci::discover() {
        Ok(pci) => output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: TARGET {:04x}:{:02x}:{:02x}.{} {:?} GPU_WRITES=0\n",
            pci.segment, pci.bdf.bus, pci.bdf.device, pci.bdf.function, pci.encoding
        ))?,
        Err(status) => output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: PCI_RESULT {:?} GPU_WRITES=0\n",
            status
        ))?,
    }
    let volume = protocol::<LoadedImage>(boot::image_handle())?
        .device()
        .ok_or(Status::NOT_FOUND)?;
    for (control, filename, components) in [
        (
            "ZERO",
            cstr16!("\\EFI\\CMP40\\ZEROCHK.EFI"),
            ["EFI", "CMP40", "ZEROCHK.EFI"],
        ),
        (
            "FULL",
            cstr16!("\\EFI\\CMP40\\FULLCHK.EFI"),
            ["EFI", "CMP40", "FULLCHK.EFI"],
        ),
    ] {
        output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: LOAD_BEGIN {} route=Firmware\n",
            control
        ))?;
        record_load(
            output,
            control,
            "Firmware",
            filename,
            load_from_volume(volume, filename),
        )?;
        output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: LOAD_BEGIN {} route=RawBuffer\n",
            control
        ))?;
        let result = (|| {
            let source = Source::own(volume)?;
            let bytes = source.read(&components, 0x02000000)?;
            output.checkpoint(format_args!(
                "CMPUNLOCKER-ENTRYCHECK: FILE {} bytes={}\n",
                control,
                bytes.len()
            ))?;
            load_from_buffer(source.device_path()?, filename, &bytes)
        })();
        record_load(output, control, "RawBuffer", filename, result)?;
    }
    output.checkpoint(format_args!(
        "CMPUNLOCKER-ENTRYCHECK: WINDOWS_PREPARE_BEGIN\n"
    ))?;
    match handoff::windows_prepared() {
        Ok(prepared) => {
            let recorded = output.checkpoint(format_args!(
                "CMPUNLOCKER-ENTRYCHECK: WINDOWS_PREPARED source={:?} CHILD_STARTS=0\n",
                prepared.source
            ));
            let unloaded = boot::unload_image(prepared.image).map_err(|error| error.status());
            recorded?;
            unloaded?;
        }
        Err(status) => output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: WINDOWS_RESULT {:?} CHILD_STARTS=0\n",
            status
        ))?,
    }
    output.checkpoint(format_args!(
        "CMPUNLOCKER-ENTRYCHECK: READY GPU_WRITES=0 CHILD_STARTS=0\n"
    ))
}

fn record_load(
    output: &mut Output,
    control: &str,
    route: &str,
    filename: &CStr16,
    result: Result<Handle, Status>,
) -> Result<(), Status> {
    match result {
        Ok(image) => {
            // Always unload even when recording the result fails.
            let recorded = output.checkpoint(format_args!(
                "CMPUNLOCKER-ENTRYCHECK: LOAD_OK {} route={} file={:?} CHILD_STARTS=0\n",
                control, route, filename
            ));
            let unloaded = boot::unload_image(image).map_err(|error| error.status());
            recorded?;
            unloaded
        }
        Err(status) => output.checkpoint(format_args!(
            "CMPUNLOCKER-ENTRYCHECK: LOAD_REFUSED {} route={} {:?}\n",
            control, route, status
        )),
    }
}

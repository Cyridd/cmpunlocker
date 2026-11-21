// SPDX-License-Identifier: MIT
use crate::image::protocol;
use cmp40_efi_core::pci::{self, Bdf, Config, Encoding};
use core::arch::asm;
use core::ffi::c_void;
use uefi::boot::{self, ScopedProtocol, SearchType};
use uefi::proto::unsafe_protocol;
use uefi::{Identify, Status};

type Read = unsafe extern "efiapi" fn(*const RootBridge, u32, u64, usize, *mut c_void) -> Status;
#[cfg(feature = "cpu-rm")]
type Write = unsafe extern "efiapi" fn(*const RootBridge, u32, u64, usize, *const c_void) -> Status;
#[cfg(not(feature = "cpu-rm"))]
type Write = usize; // opaque in the read-only build

#[repr(C)]
#[unsafe_protocol("2f707ebb-4a1a-11d4-9a38-0090273fc14d")]
struct RootBridge {
    prefix: [usize; 7], // Parent, PollMem/PollIo, Mem and Io read/write
    read: Read,
    write: Write,
    suffix: [usize; 9], // CopyMem through Configuration
    segment: u32,
}
const _: () = assert!(core::mem::offset_of!(RootBridge, read) == 7 * 8);
const _: () = assert!(core::mem::offset_of!(RootBridge, write) == 8 * 8);
const _: () = assert!(core::mem::offset_of!(RootBridge, segment) == 18 * 8);
const _: () = assert!(core::mem::size_of::<RootBridge>() == 19 * 8);

pub struct Pci {
    root: Option<ScopedProtocol<RootBridge>>,
    pub encoding: Encoding,
    pub segment: u32,
    pub bdf: Bdf,
}

impl Pci {
    pub fn discover() -> Result<Self, Status> {
        match boot::locate_handle_buffer(SearchType::ByProtocol(&RootBridge::GUID)) {
            Ok(handles) => {
                for &handle in handles.iter() {
                    for encoding in [Encoding::Standard, Encoding::Compact] {
                        let root = match protocol::<RootBridge>(handle) {
                            Ok(root) => root,
                            Err(_) => continue,
                        };
                        let segment = root.segment;
                        let mut candidate = Self {
                            root: Some(root),
                            encoding,
                            segment,
                            bdf: Bdf {
                                bus: 0,
                                device: 0,
                                function: 0,
                            },
                        };
                        if let Some(bdf) = pci::find(&mut candidate, encoding) {
                            candidate.bdf = bdf;
                            return Ok(candidate);
                        }
                    }
                }
            }
            Err(error) => uefi::println!(
                "CMP40-CPU-RM: ROOT_BRIDGE_UNAVAILABLE {:?}; probing CF8",
                error.status()
            ),
        }
        let mut candidate = Self {
            root: None,
            encoding: Encoding::Cf8,
            segment: 0,
            bdf: Bdf {
                bus: 0,
                device: 0,
                function: 0,
            },
        };
        candidate.bdf = pci::find(&mut candidate, Encoding::Cf8).ok_or(Status::NOT_FOUND)?;
        Ok(candidate)
    }

    #[cfg(feature = "cpu-rm")]
    pub fn enable_bar(&mut self) -> Result<u32, Status> {
        if self.bdf.function != 0 {
            return Err(Status::UNSUPPORTED);
        }
        let bdf = self.bdf;
        // Recheck through the same selected route before any GPU config write.
        if self.read32(bdf, 0) != pci::CMP40_ID {
            return Err(Status::DEVICE_ERROR);
        }
        pci::enable_bar(self, bdf).map_err(|_| Status::DEVICE_ERROR)
    }
}

impl Config for Pci {
    fn read32(&mut self, bdf: Bdf, offset: u8) -> u32 {
        let address = self.encoding.address(bdf, offset);
        if let Some(root) = self.root.as_ref() {
            let mut value = u32::MAX;
            // SAFETY: Layout assertions above match the standard x64 protocol.
            // Live GetProtocol borrow, UINT32 width=2, one valid DWORD output.
            let status = unsafe {
                (root.read)(
                    &**root,
                    2,
                    address,
                    1,
                    core::ptr::from_mut(&mut value).cast(),
                )
            };
            if status.is_error() {
                u32::MAX
            } else {
                value
            }
        } else {
            let value: u32;
            // SAFETY: x86-64 UEFI runs at firmware privilege. CF8/CFC is only
            // used for segment zero after both RootBridge encodings failed.
            unsafe {
                asm!("out dx, eax",in("dx")0xcf8u16,in("eax")address as u32,options(nostack,preserves_flags));
                asm!("in eax, dx",in("dx")0xcfcu16,out("eax")value,options(nostack,preserves_flags));
            }
            value
        }
    }

    #[cfg(feature = "cpu-rm")]
    fn write32(&mut self, bdf: Bdf, offset: u8, value: u32) {
        let address = self.encoding.address(bdf, offset);
        if let Some(root) = self.root.as_ref() {
            // SAFETY: Same protocol/width/buffer contract as read32. Writes are
            // limited by the core policy to the selected target's command.
            // Completion is decided by the command register readback.
            let _ =
                unsafe { (root.write)(&**root, 2, address, 1, core::ptr::from_ref(&value).cast()) };
        } else {
            // SAFETY: Selected segment-zero CF8 route; same config DWORD that
            // was read immediately before the policy's write/readback gate.
            unsafe {
                asm!("out dx, eax",in("dx")0xcf8u16,in("eax")address as u32,options(nostack,preserves_flags));
                asm!("out dx, eax",in("dx")0xcfcu16,in("eax")value,options(nostack,preserves_flags));
            }
        }
    }
    #[cfg(not(feature = "cpu-rm"))]
    fn write32(&mut self, _: Bdf, _: u8, _: u32) {
        panic!("PCI writes are unavailable in the read-only build");
    }
}

#[cfg(feature = "cpu-rm")]
pub struct Mmio {
    base: usize,
}
#[cfg(feature = "cpu-rm")]
impl Mmio {
    /// The caller has verified the target and enabled its preassigned BAR0.
    /// This adapter deliberately preserves direct BAR access and identity DMA
    /// from the C baseline; it does not establish IOMMU reachability.
    ///
    /// # Safety
    /// The caller must provide a live, assigned CMP40HX BAR0 with at least
    /// 9 MiB of CPU-accessible MMIO, and retain exclusive transaction access.
    pub unsafe fn new(base: u32) -> Result<Self, Status> {
        if base == 0 || base & 15 != 0 || u64::from(base) + 0x900000 > 0x100000000 {
            return Err(Status::UNSUPPORTED);
        }
        Ok(Self {
            base: base as usize,
        })
    }
    pub fn read32(&mut self, offset: u32) -> u32 {
        assert!(offset < 0x900000 && offset.is_multiple_of(4));
        // SAFETY: Live verified BAR0 window and aligned register offset. Each
        // access is volatile; no reference to device memory is constructed.
        unsafe { core::ptr::read_volatile((self.base + offset as usize) as *const u32) }
    }
    pub fn write32(&mut self, offset: u32, value: u32) {
        assert!(offset < 0x900000 && offset.is_multiple_of(4));
        // SAFETY: Same verified window as read32, exclusively used by this
        // one transaction. Callers do not mutate PCI resources under it.
        unsafe { core::ptr::write_volatile((self.base + offset as usize) as *mut u32, value) };
    }
    pub fn flush() {
        // SAFETY: Firmware privilege permits WBINVD. Omitting nomem supplies
        // the compiler memory barrier required by the existing DMA sequence.
        unsafe { asm!("wbinvd", options(nostack, preserves_flags)) };
    }
}

// SPDX-License-Identifier: MIT
use crate::image::protocol;
#[cfg(feature = "unlock")]
use alloc::vec::Vec;
use cmp40_efi_core::pci::{self, Bdf, Config, Encoding};
#[cfg(feature = "unlock")]
use cmp40_efi_core::rebar::{self, Bridge, Range};
use core::arch::asm;
use core::ffi::c_void;
use uefi::boot::{self, ScopedProtocol, SearchType};
use uefi::proto::unsafe_protocol;
use uefi::{Identify, Status};

type Read = unsafe extern "efiapi" fn(*const RootBridge, u32, u64, usize, *mut c_void) -> Status;
#[cfg(feature = "unlock")]
type Write = unsafe extern "efiapi" fn(*const RootBridge, u32, u64, usize, *const c_void) -> Status;
#[cfg(not(feature = "unlock"))]
type Write = usize; // opaque in the read-only build
#[cfg(feature = "unlock")]
type Configuration = unsafe extern "efiapi" fn(*const RootBridge, *mut *const u8) -> Status;
#[cfg(not(feature = "unlock"))]
type Configuration = usize;

#[repr(C)]
#[unsafe_protocol("2f707ebb-4a1a-11d4-9a38-0090273fc14d")]
struct RootBridge {
    prefix: [usize; 7], // Parent, PollMem/PollIo, Mem and Io read/write
    read: Read,
    write: Write,
    suffix: [usize; 8], // CopyMem through SetAttributes
    configuration: Configuration,
    segment: u32,
}
const _: () = assert!(core::mem::offset_of!(RootBridge, read) == 7 * 8);
const _: () = assert!(core::mem::offset_of!(RootBridge, write) == 8 * 8);
const _: () = assert!(core::mem::offset_of!(RootBridge, segment) == 18 * 8);
const _: () = assert!(core::mem::offset_of!(RootBridge, configuration) == 17 * 8);
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
                "CMPUNLOCKER: ROOT_BRIDGE_UNAVAILABLE {:?}; probing CF8",
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

    #[cfg(feature = "unlock")]
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

    #[cfg(feature = "gen2-experiment")]
    pub fn write_control16(&mut self, bdf: Bdf, offset: u8, value: u16) -> bool {
        if offset & 1 != 0 || offset > 0xfe {
            return false;
        }
        let address = self.encoding.address(bdf, offset);
        if let Some(root) = self.root.as_ref() {
            // SAFETY: Live asserted x64 RootBridgeIo layout, UINT16 width=1,
            // aligned valid WORD buffer. Core restricts these writes to Link
            // Control/Control2; adjacent W1C status is never included.
            let status =
                unsafe { (root.write)(&**root, 1, address, 1, core::ptr::from_ref(&value).cast()) };
            !status.is_error()
        } else {
            let port = 0xcfcu16 + u16::from(offset & 2);
            // SAFETY: Firmware-privileged segment-zero CF8 access. Select the
            // aligned DWORD address, then store only the requested WORD.
            unsafe {
                asm!("out dx, eax", in("dx")0xcf8u16, in("eax")address as u32, options(nostack,preserves_flags));
                asm!("out dx, ax", in("dx")port, in("ax")value, options(nostack,preserves_flags));
            }
            true
        }
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

    #[cfg(feature = "unlock")]
    fn write32(&mut self, bdf: Bdf, offset: u8, value: u32) {
        let address = self.encoding.address(bdf, offset);
        if let Some(root) = self.root.as_ref() {
            // SAFETY: Same protocol/width/buffer contract as read32. Writes are
            // limited to the target and its selected upstream bridge by the
            // core policies. BAR0 is never relocated.
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
    #[cfg(not(feature = "unlock"))]
    fn write32(&mut self, _: Bdf, _: u8, _: u32) {
        panic!("PCI writes are unavailable in the read-only build");
    }
}

/// ReBAR reuses the discovery encoding on every root, with CF8 restricted
/// to segment zero. Segment filtering prevents a bridge on another PCI
/// segment from being mistaken for the target's parent.
#[cfg(feature = "unlock")]
pub struct RebarRoots {
    roots: Vec<Pci>,
}
#[cfg(feature = "unlock")]
impl RebarRoots {
    pub fn collect(target: &Pci) -> Result<Self, Status> {
        let mut roots = Vec::new();
        roots
            .try_reserve_exact(8)
            .map_err(|_| Status::OUT_OF_RESOURCES)?;
        if target.encoding == Encoding::Cf8 {
            roots.push(Pci {
                root: None,
                encoding: Encoding::Cf8,
                segment: 0,
                bdf: target.bdf,
            });
        } else {
            let handles = boot::locate_handle_buffer(SearchType::ByProtocol(&RootBridge::GUID))
                .map_err(|e| e.status())?;
            for &handle in handles.iter() {
                if roots.len() == 8 {
                    break;
                }
                let root = match protocol::<RootBridge>(handle) {
                    Ok(root) if root.segment == target.segment => root,
                    _ => continue,
                };
                roots.push(Pci {
                    root: Some(root),
                    encoding: target.encoding,
                    segment: target.segment,
                    bdf: target.bdf,
                });
            }
        }
        Ok(Self { roots })
    }
    pub fn count(&self) -> usize {
        self.roots.len()
    }
    pub fn read(&mut self, bridge: Bridge, offset: u8) -> u32 {
        self.roots
            .get_mut(bridge.root)
            .map_or(u32::MAX, |pci| pci.read32(bridge.bdf, offset))
    }
    pub fn write(&mut self, bridge: Bridge, offset: u8, value: u32) {
        if let Some(pci) = self.roots.get_mut(bridge.root) {
            pci.write32(bridge.bdf, offset, value);
        }
    }
    #[cfg(feature = "gen2-experiment")]
    pub fn write_control16(&mut self, bridge: Bridge, offset: u8, value: u16) -> bool {
        self.roots
            .get_mut(bridge.root)
            .is_some_and(|pci| pci.write_control16(bridge.bdf, offset, value))
    }
    pub fn aperture(&self, index: usize) -> Option<Range> {
        let root = self.roots.get(index)?.root.as_ref()?;
        let mut pointer = core::ptr::null();
        // SAFETY: Valid x64 protocol layout, live GetProtocol borrow, valid
        // output pointer. UEFI owns the returned ACPI resource template.
        let status = unsafe { (root.configuration)(&**root, &mut pointer) };
        if status.is_error() || pointer.is_null() {
            return None;
        }
        let mut storage = [0u8; 46];
        let mut best: Option<Range> = None;
        let mut consumed = 0usize;
        for _ in 0..128 {
            if consumed > 0x10000 - 3 {
                break;
            }
            // SAFETY: Configuration's success contract supplies a valid
            // descriptor stream through its end tag. Read the header before
            // the payload; cap traversal to 128 descriptors / 64 KiB.
            let tag = unsafe { pointer.read() };
            if tag == 0x79 {
                break;
            }
            let size = if tag & 0x80 == 0 {
                usize::from(tag & 7) + 1
            } else {
                // SAFETY: A large ACPI descriptor has a two-byte length
                // immediately after its tag. The end tag was handled above.
                usize::from(unsafe {
                    u16::from_le_bytes([pointer.add(1).read(), pointer.add(2).read()])
                }) + 3
            };
            if size > 0x10000 - consumed {
                break;
            }
            let wanted = match tag {
                0x8a if size >= 46 => 46,
                0x87 if size >= 30 => 30,
                _ => 0,
            };
            if wanted != 0 {
                // SAFETY: The validated descriptor declares at least wanted
                // bytes. Copy only the fixed fields used by parse_range.
                unsafe { core::ptr::copy_nonoverlapping(pointer, storage.as_mut_ptr(), wanted) };
                // Normalize only the copied descriptor's length; extension
                // bytes are irrelevant to the three fixed address fields.
                storage[1..3].copy_from_slice(&((wanted - 3) as u16).to_le_bytes());
                if let (Some(cur), _) = rebar::parse_range(&storage[..wanted]) {
                    if best.is_none_or(|old| {
                        let high = cur.max >= 0x100000000;
                        let old_high = old.max >= 0x100000000;
                        (high && !old_high)
                            || (high == old_high && cur.max - cur.min > old.max - old.min)
                    }) {
                        best = Some(cur);
                    }
                }
            }
            consumed += size;
            // SAFETY: Remains within the protocol-owned descriptor stream,
            // bounded above. The next iteration checks its header/end tag.
            pointer = unsafe { pointer.add(size) };
        }
        best
    }
}

#[cfg(feature = "unlock")]
pub struct Mmio {
    base: usize,
}
#[cfg(feature = "unlock")]
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

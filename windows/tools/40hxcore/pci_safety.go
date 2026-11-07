package hxcore

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
	"syscall"
)

type pciReadFunc func(bdf, reg uint32) (uint32, error)

// UpdatePCI16 requires a successful read and writes only the control word,
// leaving the adjacent PCI status (often write-one-to-clear) untouched.
func UpdatePCI16(wh syscall.Handle, bdf, reg uint32, clear, set uint16) error {
	return updatePCI16(func(b, r uint32) (uint32, error) { return PciRd(wh, b, r) },
		func(b, r uint32, v []byte) error { return PciWr(wh, b, r, v) }, bdf, reg, clear, set)
}

func updatePCI16(read pciReadFunc, write func(uint32, uint32, []byte) error,
	bdf, reg uint32, clear, set uint16) error {
	if reg > 0xFC || reg&1 != 0 {
		return fmt.Errorf("invalid PCI control offset %x", reg)
	}
	cur, err := read(bdf, reg)
	if err != nil {
		return err
	}
	if cur == 0xFFFFFFFF {
		return fmt.Errorf("PCI control unavailable at BDF %x", bdf)
	}
	want := (uint16(cur) &^ clear) | set
	return write(bdf, reg, []byte{byte(want), byte(want >> 8)})
}

// GPUBar0 validates the target identity and reads its assigned MMIO address.
// TU106 BAR0 is a 32-bit memory BAR. Never guess an address on read failure,
// or truncate an unexpected 64-bit BAR into a different device's mapping.
func GPUBar0(wh syscall.Handle, bdf uint32) (uint64, error) {
	return gpuBar0(func(b, r uint32) (uint32, error) { return PciRd(wh, b, r) }, bdf)
}

func gpuBar0(read pciReadFunc, bdf uint32) (uint64, error) {
	id, err := read(bdf, 0)
	if err != nil || id != 0x1F0B10DE {
		return 0, fmt.Errorf("CMP40HX identity unavailable at BDF %04x: %08x (%v)", bdf, id, err)
	}
	raw, err := read(bdf, 0x10)
	if err != nil || raw == 0xFFFFFFFF || raw&7 != 0 || raw&0xFFFFFFF0 < 0x100000 {
		return 0, fmt.Errorf("invalid or unsupported CMP40HX BAR0: %08x (%v)", raw, err)
	}
	return uint64(raw & 0xFFFFFFF0), nil
}

// FindGPUExclusiveRoot permits mutations only on a direct bus-0 root port
// serving this GPU alone. Never retrain/disable a shared downstream subtree:
// it may contain a SATA/NVMe controller. Discovery for diagnostics is separate.
func FindGPUExclusiveRoot(wh syscall.Handle, gpuBDF uint32) uint32 {
	present, err := presentPCIBDFs()
	if err != nil {
		return 0xFFFFFFFF
	}
	return findGPUExclusiveRoot(func(b, r uint32) (uint32, error) { return PciRd(wh, b, r) }, gpuBDF, present)
}

// WinRing0 may report a missing function as an IOCTL error, rather than an
// all-ones read. Enumerate PRESENT PCI devices through Windows first so a
// missing slot is not mistaken for a failed read of an existing controller.
// Missing/incomplete inventory disables root-port mutations conservatively.
func presentPCIBDFs() ([]uint32, error) {
	set, err := windows.SetupDiGetClassDevsEx(nil, "PCI", 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return nil, err
	}
	defer windows.SetupDiDestroyDeviceInfoList(set)
	var result []uint32
	seen := make(map[uint32]bool)
	for i := 0; ; i++ {
		info, err := windows.SetupDiEnumDeviceInfo(set, i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			break
		}
		if err != nil {
			return nil, err
		}
		busValue, err := windows.SetupDiGetDeviceRegistryProperty(set, info, windows.SPDRP_BUSNUMBER)
		if err != nil {
			return nil, err
		}
		addressValue, err := windows.SetupDiGetDeviceRegistryProperty(set, info, windows.SPDRP_ADDRESS)
		if err != nil {
			return nil, err
		}
		bus, okBus := busValue.(uint32)
		address, okAddress := addressValue.(uint32)
		if !okBus || !okAddress || bus > 255 || address>>16 > 31 || address&0xFFFF > 7 {
			return nil, fmt.Errorf("unusable present PCI device address")
		}
		bdf := bus<<8 | (address>>16)<<3 | address&7
		if seen[bdf] {
			return nil, fmt.Errorf("ambiguous/duplicate present PCI BDF %x", bdf)
		}
		seen[bdf] = true
		result = append(result, bdf)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("present PCI inventory is empty")
	}
	return result, nil
}

func findGPUExclusiveRoot(read pciReadFunc, gpuBDF uint32, present []uint32) uint32 {
	port, err := findGPUExclusivePort(read, gpuBDF, present)
	if err != nil || port.Type != 4 || port.BDF>>8 != 0 {
		return 0xFFFFFFFF
	}
	return port.BDF
}

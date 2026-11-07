package hxcore

import (
	"fmt"
	"syscall"
)

// PCIePort is the immediate downstream-facing port of a verified GPU-only
// bus. A chipset downstream port is suitable for ordinary retraining, but
// does not acquire permission for Link Disable through this API.
type PCIePort struct {
	BDF          uint32
	VendorDevice uint32
	Capability   uint32
	Type         uint32
}

func (p PCIePort) String() string {
	kind := "root port"
	if p.Type == 6 {
		kind = "downstream port"
	}
	return fmt.Sprintf("%02x:%02x.%x (%s)", p.BDF>>8, (p.BDF>>3)&31, p.BDF&7, kind)
}

func FindGPUExclusivePort(wh syscall.Handle, gpuBDF uint32) (PCIePort, error) {
	present, err := presentPCIBDFs()
	if err != nil {
		return PCIePort{}, fmt.Errorf("PCI inventory unavailable: %w", err)
	}
	return findGPUExclusivePort(func(b, r uint32) (uint32, error) { return PciRd(wh, b, r) }, gpuBDF, present)
}

func findGPUExclusivePort(read pciReadFunc, gpuBDF uint32, present []uint32) (PCIePort, error) {
	bus := gpuBDF >> 8
	if bus == 0 || gpuBDF > 0xFFFF {
		return PCIePort{}, fmt.Errorf("invalid GPU BDF %x", gpuBDF)
	}
	seen := make(map[uint32]bool)
	for _, bdf := range present {
		if bdf > 0xFFFF || seen[bdf] {
			return PCIePort{}, fmt.Errorf("ambiguous PCI inventory at BDF %x", bdf)
		}
		seen[bdf] = true
	}
	if !seen[gpuBDF] {
		return PCIePort{}, fmt.Errorf("GPU %04x is absent from the PCI inventory", gpuBDF)
	}
	id, err := read(gpuBDF, 0)
	if err != nil || id != 0x1F0B10DE {
		return PCIePort{}, fmt.Errorf("CMP40HX identity unavailable at %04x", gpuBDF)
	}
	// A port with secondary == subordinate == GPU bus must serve only the
	// GPU and its non-storage, non-bridge NVIDIA functions. Other chipset
	// branches are outside this link and do not make its leaf bus shared.
	for _, bdf := range present {
		if bdf>>8 != bus {
			continue
		}
		if bdf&^7 != gpuBDF&^7 {
			return PCIePort{}, fmt.Errorf("GPU bus %02x is shared with %04x", bus, bdf)
		}
		id, err := read(bdf, 0)
		if err != nil || id&0xFFFF != 0x10DE {
			return PCIePort{}, fmt.Errorf("GPU function identity unavailable at %04x", bdf)
		}
		hdr, err := read(bdf, 0x0C)
		if err != nil || hdr == 0xFFFFFFFF || (hdr>>16)&0x7F != 0 {
			return PCIePort{}, fmt.Errorf("GPU bus contains an unverified endpoint at %04x", bdf)
		}
		if bdf != gpuBDF {
			cls, err := read(bdf, 8)
			if err != nil || cls == 0xFFFFFFFF || cls>>24 == 1 || cls>>24 == 6 {
				return PCIePort{}, fmt.Errorf("storage/bridge or unreadable GPU function at %04x", bdf)
			}
		}
	}

	var result PCIePort
	found := false
	for _, bdf := range present {
		if bdf>>8 == bus {
			continue
		}
		id, err := read(bdf, 0)
		if err != nil || id&0xFFFF == 0 || id&0xFFFF == 0xFFFF {
			return PCIePort{}, fmt.Errorf("present PCI device %04x is unreadable", bdf)
		}
		cls, err := read(bdf, 8)
		if err != nil || cls == 0xFFFFFFFF {
			return PCIePort{}, fmt.Errorf("PCI class unavailable at %04x", bdf)
		}
		if (cls>>16)&0xFFFF != 0x0604 {
			continue
		}
		buses, err := read(bdf, 0x18)
		if err != nil || buses == 0xFFFFFFFF {
			return PCIePort{}, fmt.Errorf("bridge bus numbers unavailable at %04x", bdf)
		}
		if (buses>>8)&255 != bus {
			continue // An ancestor/chipset uplink is never a substitute.
		}
		if found {
			return PCIePort{}, fmt.Errorf("multiple bridges claim GPU bus %02x", bus)
		}
		if buses&255 != bdf>>8 || bdf>>8 >= bus || (buses>>16)&255 != bus {
			return PCIePort{}, fmt.Errorf("bridge %04x has a shared or inconsistent bus range", bdf)
		}
		hdr, err := read(bdf, 0x0C)
		if err != nil || hdr == 0xFFFFFFFF || (hdr>>16)&0x7F != 1 {
			return PCIePort{}, fmt.Errorf("bridge header unavailable at %04x", bdf)
		}
		cap, typ, err := downstreamCapability(read, bdf)
		if err != nil {
			return PCIePort{}, err
		}
		result = PCIePort{BDF: bdf, VendorDevice: id, Capability: cap, Type: typ}
		found = true
	}
	if !found {
		return PCIePort{}, fmt.Errorf("no immediate PCIe port found for GPU bus %02x", bus)
	}
	return result, nil
}

func downstreamCapability(read pciReadFunc, bdf uint32) (uint32, uint32, error) {
	ptr, err := read(bdf, 0x34)
	if err != nil {
		return 0, 0, fmt.Errorf("PCIe capability pointer unavailable at %04x", bdf)
	}
	seen := make(map[uint32]bool)
	for cap := ptr & 255; cap != 0; {
		if cap < 0x40 || cap > 0xFC || cap&3 != 0 || seen[cap] {
			return 0, 0, fmt.Errorf("invalid PCIe capability chain at %04x", bdf)
		}
		seen[cap] = true
		v, err := read(bdf, cap)
		if err != nil || v == 0xFFFFFFFF {
			return 0, 0, fmt.Errorf("PCIe capability unreadable at %04x", bdf)
		}
		if v&255 == 0x10 {
			typ := (v >> 20) & 15
			if (typ != 4 && typ != 6) || (v>>16)&15 < 2 || cap > 0xCC {
				return 0, 0, fmt.Errorf("%04x is not a usable PCIe v2+ root/downstream port", bdf)
			}
			return cap, typ, nil
		}
		cap = (v >> 8) & 255
	}
	return 0, 0, fmt.Errorf("no PCIe capability at %04x", bdf)
}

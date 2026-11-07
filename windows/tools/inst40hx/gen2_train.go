package main

import (
	hxcore "40hxcore"
	"errors"
	"fmt"
	"syscall"
	"time"
)

func gen2TrainUpstream(wh syscall.Handle, gpu uint32, port hxcore.PCIePort) (uint32, error) {
	validate := func() error {
		current, err := hxcore.FindGPUExclusivePort(wh, gpu)
		if err != nil {
			return err
		}
		if current != port {
			return fmt.Errorf("GPU upstream port changed; refusing PCI writes")
		}
		return nil
	}
	return gen2TrainVerifiedPort(gpu, hxcore.PcieCap(wh, gpu), port,
		func(b, r uint32) (uint32, error) { return hxcore.PciRd(wh, b, r) },
		func(b, r uint32, v []byte) error { return hxcore.PciWr(wh, b, r, v) },
		validate, time.Sleep)
}

// Retrain only the immediate downstream-facing port. The GPU's Link Control
// Retrain bit is not a substitute for this operation. Failure restores only
// the TLS fields owned by this attempt, after revalidating the topology.
func gen2TrainVerifiedPort(gpu, gpuCap uint32, port hxcore.PCIePort,
	read func(uint32, uint32) (uint32, error), write func(uint32, uint32, []byte) error,
	validate func() error, pause func(time.Duration)) (speed uint32, resultErr error) {
	if gpuCap < 0x40 || gpuCap > 0xCC || gpuCap&3 != 0 ||
		port.Capability < 0x40 || port.Capability > 0xCC || port.Capability&3 != 0 ||
		(port.Type != 4 && port.Type != 6) || gpu > 0xFFFF || port.BDF > 0xFFFF || gpu == port.BDF {
		return 0, fmt.Errorf("invalid GPU/upstream PCIe capability")
	}
	if err := validate(); err != nil {
		return 0, err
	}
	devices := []struct{ bdf, cap, typ uint32 }{{gpu, gpuCap, 0}, {port.BDF, port.Capability, port.Type}}
	rd := func(bdf, reg uint32) (uint32, error) {
		v, err := read(bdf, reg)
		if err != nil || v == 0xFFFFFFFF {
			return 0, fmt.Errorf("PCI read failed at %04x+%02x: %08x (%v)", bdf, reg, v, err)
		}
		return v, nil
	}
	update := func(bdf, reg uint32, mask, value uint16) error {
		old, err := rd(bdf, reg)
		if err != nil {
			return err
		}
		want := (uint16(old) &^ mask) | (value & mask)
		if err := write(bdf, reg, []byte{byte(want), byte(want >> 8)}); err != nil {
			return err
		}
		rb, err := rd(bdf, reg)
		if err != nil || uint16(rb)&mask != want&mask {
			return fmt.Errorf("PCI control readback failed at %04x+%02x (%v)", bdf, reg, err)
		}
		return nil
	}
	var originalTLS [2]uint16
	for i, dev := range devices {
		hdr, err := rd(dev.bdf, dev.cap)
		if err != nil || hdr&255 != 0x10 || (hdr>>16)&15 < 2 || (hdr>>20)&15 != dev.typ {
			return 0, fmt.Errorf("PCIe identity/type changed at %04x", dev.bdf)
		}
		cap, err := rd(dev.bdf, dev.cap+0x0C)
		if err != nil || !hxcore.Gen2Reached(cap&15) {
			return 0, fmt.Errorf("%04x does not advertise Gen2", dev.bdf)
		}
		cap2, err := rd(dev.bdf, dev.cap+0x2C)
		// Older Gen2 components may leave the speed vector zero and use
		// LNKCAP's maximum-speed field. A nonzero vector must include Gen2.
		if err != nil || (cap2&0xFE != 0 && cap2&4 == 0) || (cap2&0xFE == 0 && cap&15 != 2) {
			return 0, fmt.Errorf("%04x lacks the Gen2 supported-speed bit", dev.bdf)
		}
		ctl2, err := rd(dev.bdf, dev.cap+0x30)
		if err != nil || ctl2&0x410 != 0 || ctl2&15 == 0 || ctl2&15 > 6 {
			return 0, fmt.Errorf("%04x target unavailable or compliance mode enabled", dev.bdf)
		}
		originalTLS[i] = uint16(ctl2) & 15
	}
	readLink := func() (uint32, uint32, error) {
		var speeds, widths [2]uint32
		for i, dev := range devices {
			v, err := rd(dev.bdf, dev.cap+0x10)
			if err != nil || v&0x10 != 0 {
				return 0, 0, fmt.Errorf("link inaccessible or disabled at %04x", dev.bdf)
			}
			speeds[i], widths[i] = hxcore.DecodeLinkStatus(v >> 16)
			fmt.Printf("[Gen2] %04x LNKCTL/STA=%08x current=Gen%d x%d\n", dev.bdf, v, speeds[i], widths[i])
			if speeds[i] == 0 {
				return 0, 0, fmt.Errorf("link is training or its status is invalid at %04x", dev.bdf)
			}
		}
		if speeds[0] != speeds[1] || widths[0] != widths[1] {
			return 0, 0, fmt.Errorf("GPU/upstream link observations disagree")
		}
		return speeds[0], widths[0], nil
	}
	_, width, err := readLink()
	if err != nil {
		return 0, err
	}
	var changed [2]bool
	keep := false
	defer func() {
		if (!changed[0] && !changed[1]) || keep {
			return
		}
		if err := validate(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("TLS restore skipped: %w", err))
			return
		}
		for i, dev := range devices {
			if !changed[i] {
				continue
			}
			current, err := rd(dev.bdf, dev.cap+0x30)
			if err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("TLS restore read failed: %w", err))
				continue
			}
			if uint16(current)&15 == originalTLS[i] {
				continue
			}
			if current&15 != 2 {
				resultErr = errors.Join(resultErr, fmt.Errorf("TLS changed externally at %04x; restore skipped", dev.bdf))
				continue
			}
			if err := update(dev.bdf, dev.cap+0x30, 15, originalTLS[i]); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("TLS restore failed at %04x: %w", dev.bdf, err))
			} else {
				fmt.Printf("[Gen2] %04x original TLS=%d restored\n", dev.bdf, originalTLS[i])
			}
		}
	}()
	for attempt := 0; attempt < 6; attempt++ {
		if err := validate(); err != nil {
			return 0, err
		}
		_, currentWidth, err := readLink()
		if err != nil || currentWidth != width {
			return 0, fmt.Errorf("link changed before retrain (%v)", err)
		}
		for i, dev := range devices {
			changed[i] = true // An IOCTL error may follow a submitted write.
			if err := update(dev.bdf, dev.cap+0x30, 15, 2); err != nil {
				return 0, err
			}
		}
		// Clear any old request using a control WORD, then set Retrain Link.
		if err := update(port.BDF, port.Capability+0x10, 0x20, 0); err != nil {
			return 0, err
		}
		pause(300 * time.Millisecond)
		if err := validate(); err != nil {
			return 0, err
		}
		ctl, err := rd(port.BDF, port.Capability+0x10)
		if err != nil || ctl&0x10 != 0 {
			return 0, fmt.Errorf("upstream disappeared before retrain")
		}
		want := uint16(ctl) | 0x20
		// Retrain Link self-clears, so a set-bit readback is not an acceptance test.
		if err := write(port.BDF, port.Capability+0x10, []byte{byte(want), byte(want >> 8)}); err != nil {
			return 0, err
		}
		fmt.Printf("[Gen2] Retrain #%d through %s\n", attempt+1, port)
		pause(2200 * time.Millisecond)
		if err := validate(); err != nil {
			return 0, err
		}
		speed, currentWidth, err = readLink()
		if err != nil || currentWidth != width {
			return 0, fmt.Errorf("link unverified after retrain (%v)", err)
		}
		if hxcore.Gen2Reached(speed) {
			keep = true
			return speed, nil
		}
	}
	return speed, nil // A valid Gen1 result is not an I/O error or success.
}

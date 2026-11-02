package main

import (
	"fmt"
	"time"
)

// The caller must first prove that root exclusively serves this GPU and set
// TLS while the GPU is reachable. The cycle accesses only root configuration;
// an endpoint transaction while LD is asserted could prevent its clear.
func gen2LinkDisableCycle(root, reg uint32,
	read func(uint32, uint32) (uint32, error), write func(uint32, uint32, []byte) error,
	pause func(time.Duration)) error {
	if reg > 0xFC || reg&3 != 0 {
		return fmt.Errorf("invalid root Link Control offset %x", reg)
	}
	ctl, err := read(root, reg)
	if err != nil {
		return err
	}
	if ctl == 0xFFFFFFFF || ctl&0x10 != 0 {
		return fmt.Errorf("root Link Control unavailable or already disabled: %08x", ctl)
	}
	lo := uint16(ctl)
	set, clear := lo|0x10, lo&^0x10
	assertErr := write(root, reg, []byte{byte(set), byte(set >> 8)})
	if assertErr == nil {
		pause(500 * time.Millisecond)
	}
	// An ambiguous failed submission may have asserted LD. Always attempt its
	// clear, using the pre-disable control snapshot even when later reads fail.
	var clearErr error
	for attempt := 0; attempt < 2; attempt++ {
		clearErr = write(root, reg, []byte{byte(clear), byte(clear >> 8)})
		if clearErr != nil {
			continue
		}
		rb, rerr := read(root, reg)
		if rerr != nil {
			clearErr = rerr
			continue
		}
		if rb == 0xFFFFFFFF || rb&0x10 != 0 {
			clearErr = fmt.Errorf("root Link Disable clear not confirmed: %08x", rb)
			continue
		}
		if assertErr != nil {
			return fmt.Errorf("Link Disable submission failed (clear confirmed): %w", assertErr)
		}
		pause(2 * time.Second)
		return nil
	}
	return fmt.Errorf("Link Disable clear failed; further GPU accesses refused: %w", clearErr)
}

package hxcore

import "syscall"

func Gen2Reached(speed uint32) bool { return speed >= 2 && speed <= 6 }

// Gen2Status keeps an advertised target separate from an observed link speed.
func Gen2Status(speed, target uint32) string {
	switch {
	case Gen2Reached(speed):
		return "gen2_reached"
	case Gen2Reached(target):
		return "gen2_target_set"
	case speed == 1:
		return "gen1"
	default:
		return "unknown"
	}
}

func DecodeLinkStatus(raw uint32) (speed, width uint32) {
	if raw == 0xFFFFFFFF || raw&0x800 != 0 {
		return 0, 0
	}
	speed, width = raw&15, (raw>>4)&63
	if speed == 0 || speed > 6 {
		return 0, 0
	}
	switch width {
	case 1, 2, 4, 8, 12, 16, 32:
		return speed, width
	default:
		return 0, 0
	}
}

func TargetLinkSpeed(wh syscall.Handle, bdf uint32) uint32 {
	cap := PcieCap(wh, bdf)
	if cap == 0 || cap > 0xCC {
		return 0
	}
	hdr, err := PciRd(wh, bdf, cap)
	if err != nil || hdr == 0xFFFFFFFF || (hdr>>16)&15 < 2 {
		return 0
	}
	v, err := PciRd(wh, bdf, cap+0x30)
	if err != nil || v == 0xFFFFFFFF || v&15 == 0 || v&15 > 6 {
		return 0
	}
	return v & 15
}

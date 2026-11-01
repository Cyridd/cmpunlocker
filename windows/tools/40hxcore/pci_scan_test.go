package hxcore

import "testing"

func TestFindGPUPCI(t *testing.T) {
	for _, tc := range []struct {
		name             string
		known            bool
		knownBDF, target uint32
		multifunction    bool
		wantReads        int
	}{
		{"known high bus", true, 0x4200, 0x4200, false, 1},
		{"stale registry", true, 0x100, 0x1800, false, 770},
		{"no registry", false, 0, 0x1800, false, 769},
		{"multifunction GPU", false, 0, 7, true, 9},
		{"missing GPU", false, 0, 0xFFFFFFFF, false, 8192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			read := func(bdf, reg uint32) (uint32, error) {
				reads++
				if reg == 0 && bdf == tc.target {
					return 0x1F0B10DE, nil
				}
				if tc.multifunction && bdf == 0 {
					if reg == 0 {
						return 0x12348086, nil
					}
					return 1 << 23, nil
				}
				return 0xFFFFFFFF, nil
			}
			bdf, found := findGPUPCI(read, tc.knownBDF, tc.known)
			if found != (tc.target != 0xFFFFFFFF) || found && bdf != tc.target {
				t.Fatalf("got %x/%v, target %x", bdf, found, tc.target)
			}
			if reads != tc.wantReads {
				t.Fatalf("%d PCI reads, want %d", reads, tc.wantReads)
			}
		})
	}
}

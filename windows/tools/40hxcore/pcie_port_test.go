package hxcore

import (
	"errors"
	"testing"
)

func TestImmediatePortTopologies(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[[2]uint32]uint32)
		want uint32
		fail bool
	}{
		{"CPU GPU at 10:00.0", func(m map[[2]uint32]uint32) {}, 8, false},
		{"chipset GPU-only downstream", chipsetPortFixture, 0x0F08, false},
		{"chipset with storage on another branch", func(m map[[2]uint32]uint32) {
			chipsetPortFixture(m)
			m[[2]uint32{0x1100, 0}] = 0x1234144D
			m[[2]uint32{0x1100, 8}] = 0x01080200
		}, 0x0F08, false},
		{"shared leaf with storage", func(m map[[2]uint32]uint32) { m[[2]uint32{0x1008, 0}] = 0x1234144D }, 0, true},
		{"ancestor uplink only", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x18}] = 0x00200100 }, 0, true},
		{"subordinate subtree", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x18}] = 0x00201000 }, 0, true},
		{"incorrect primary bus", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x18}] |= 1 }, 0, true},
		{"upstream-facing port", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x40}] = 0x00520010 }, 0, true},
		{"v1 capability", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x40}] = 0x00410010 }, 0, true},
		{"cyclic capability list", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x40}] = 0x00004001 }, 0, true},
		{"duplicate immediate port", func(m map[[2]uint32]uint32) {
			for _, reg := range []uint32{0, 8, 12, 0x18, 0x34, 0x40} {
				m[[2]uint32{16, reg}] = m[[2]uint32{8, reg}]
			}
		}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := map[[2]uint32]uint32{
				{0x1000, 0}: 0x1F0B10DE, {0x1000, 12}: 0,
				{8, 0}: 0x14DB1022, {8, 8}: 0x06040000, {8, 12}: 0x00010000,
				{8, 0x18}: 0x00101000, {8, 0x34}: 0x40, {8, 0x40}: 0x00420010,
			}
			tc.edit(m)
			var inventory []uint32
			for key := range m {
				if key[1] == 0 {
					inventory = append(inventory, key[0])
				}
			}
			read := func(b, r uint32) (uint32, error) {
				if v, ok := m[[2]uint32{b, r}]; ok {
					return v, nil
				}
				return 0xFFFFFFFF, nil
			}
			port, err := findGPUExclusivePort(read, 0x1000, inventory)
			if (err != nil) != tc.fail || (!tc.fail && port.BDF != tc.want) {
				t.Fatalf("port=%+v err=%v", port, err)
			}
			if tc.fail {
				return
			}
			// Every register required for this proof must fail closed.
			for key := range m {
				calls := 0
				fault := func(b, r uint32) (uint32, error) {
					if key == [2]uint32{b, r} {
						calls++
						return 0, errors.New("read failed")
					}
					return read(b, r)
				}
				_, err := findGPUExclusivePort(fault, 0x1000, inventory)
				if calls > 0 && err == nil {
					t.Fatalf("read failure at %x was accepted", key)
				}
			}
			if _, err := findGPUExclusivePort(read, 0x1000, append(inventory, 0x1000)); err == nil {
				t.Fatal("duplicate inventory accepted")
			}
			if tc.want != 8 && findGPUExclusiveRoot(read, 0x1000, inventory) != 0xFFFFFFFF {
				t.Fatal("downstream port acquired Link Disable permission")
			}
		})
	}
}

func chipsetPortFixture(m map[[2]uint32]uint32) {
	// Bus 0 -> chipset subtree; immediate GPU port on bus 0x0f -> bus 0x10.
	m[[2]uint32{8, 0x18}] = 0x00200100
	for _, reg := range []uint32{0, 8, 12, 0x34} {
		m[[2]uint32{0x0F08, reg}] = m[[2]uint32{8, reg}]
	}
	m[[2]uint32{0x0F08, 0x18}] = 0x0010100F
	m[[2]uint32{0x0F08, 0x40}] = 0x00620010
}

func TestImmediatePortUnreadableDevices(t *testing.T) {
	for _, topology := range []struct {
		name      string
		port, typ uint32
	}{
		{"CPU root", 8, 4},
		{"chipset downstream", 0x1008, 6},
	} {
		t.Run(topology.name, func(t *testing.T) {
			const gpu = uint32(0x1100)
			for _, tc := range []struct {
				name string
				bdf  uint32
				fail bool
			}{
				{"later bus 1b:00.0", 0x1B00, false},
				{"possible parent bus", 0x0800, true},
				{"selected port", topology.port, true},
				{"GPU", gpu, true},
				{"GPU sibling function", gpu + 1, true},
				{"shared GPU bus", gpu + 8, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, mode := range []string{"IOCTL error", "all ones", "zero vendor"} {
						t.Run(mode, func(t *testing.T) {
							m := map[[2]uint32]uint32{
								{gpu, 0}: 0x1F0B10DE, {gpu, 12}: 0,
								{topology.port, 0}: 0x43F41022,
								{topology.port, 8}: 0x06040000, {topology.port, 12}: 0x00010000,
								{topology.port, 0x18}: 0x00111100 | topology.port>>8,
								{topology.port, 0x34}: 0x40,
								{topology.port, 0x40}: topology.typ<<20 | 0x00020010,
							}
							inventory := []uint32{gpu, topology.port}
							if tc.bdf != gpu && tc.bdf != topology.port {
								// Place the unrelated device first so finding the port early
								// cannot hide an overly broad scan.
								inventory = append([]uint32{tc.bdf}, inventory...)
							}
							faultReads := 0
							read := func(b, r uint32) (uint32, error) {
								if b == tc.bdf {
									faultReads++
									switch mode {
									case "IOCTL error":
										return 0, errors.New("config read failed")
									case "all ones":
										return 0xFFFFFFFF, nil
									default:
										return 0, nil
									}
								}
								if v, ok := m[[2]uint32{b, r}]; ok {
									return v, nil
								}
								return 0, errors.New("unexpected config read")
							}
							port, err := findGPUExclusivePort(read, gpu, inventory)
							if (err != nil) != tc.fail || (!tc.fail && port.BDF != topology.port) {
								t.Fatalf("port=%+v error=%v", port, err)
							}
							if !tc.fail && faultReads != 0 {
								t.Fatal("read configuration on a bus beyond the GPU leaf")
							}
							root := findGPUExclusiveRoot(read, gpu, inventory)
							wantRoot := uint32(0xFFFFFFFF)
							if !tc.fail && topology.typ == 4 {
								wantRoot = topology.port
							}
							if root != wantRoot {
								t.Fatalf("Link Disable port=%04x, want %04x", root, wantRoot)
							}
						})
					}
				})
			}
		})
	}
}

func TestGen2StatusDoesNotTreatTargetAsSuccess(t *testing.T) {
	for _, tc := range []struct {
		speed, target uint32
		want          string
	}{
		{1, 2, "gen2_target_set"}, {2, 2, "gen2_reached"},
		{1, 1, "gen1"}, {0, 2, "gen2_target_set"},
		{15, 15, "unknown"}, {0, 0, "unknown"},
	} {
		if got := Gen2Status(tc.speed, tc.target); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
		if Gen2Reached(tc.speed) != (tc.want == "gen2_reached") {
			t.Errorf("target/invalid speed counted as success: %+v", tc)
		}
	}
	for _, raw := range []uint32{0xFFFFFFFF, 0, 0x104F, 0x1802, 0x104A, 0x1032} {
		if speed, width := DecodeLinkStatus(raw); speed != 0 || width != 0 {
			t.Errorf("invalid/training status %08x accepted", raw)
		}
	}
	if speed, width := DecodeLinkStatus(0x1042); speed != 2 || width != 4 {
		t.Fatal("valid Gen2 x4 rejected")
	}
}

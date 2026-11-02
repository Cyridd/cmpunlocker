package hxcore

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestPCIControlWriteRequiresReadAndPreservesStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   uint32
		readErr bool
		reg     uint32
		writes  int
	}{
		{"valid control", 0xABCD1141, false, 0x70, 1},
		{"unavailable", 0xFFFFFFFF, false, 0x70, 0},
		{"read failed", 0, true, 0x70, 0},
		{"unaligned", 0, false, 0x71, 0},
		{"beyond config", 0, false, 0x100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			read := func(b, r uint32) (uint32, error) {
				if tc.readErr {
					return 0, errors.New("read failed")
				}
				return tc.value, nil
			}
			write := func(b, r uint32, data []byte) error {
				writes++
				if b != 8 || r != tc.reg || len(data) != 2 || binary.LittleEndian.Uint16(data) != 0x1142 {
					t.Fatalf("unexpected control write: %x, %x, %x", b, r, data)
				}
				return nil
			}
			err := updatePCI16(read, write, 8, tc.reg, 15, 2)
			if writes != tc.writes || (err == nil) != (tc.writes != 0) {
				t.Fatalf("writes=%d err=%v", writes, err)
			}
		})
	}
}

func TestGPUBar0RejectsUnverifiedAddresses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id, raw uint32
		failure bool
		want    uint64
	}{
		{"assigned TU106", 0x1F0B10DE, 0xF6000000, false, 0xF6000000},
		{"absent", 0xFFFFFFFF, 0xF6000000, false, 0},
		{"SATA controller", 0xA2828086, 0xF6000000, false, 0},
		{"zero BAR", 0x1F0B10DE, 0, false, 0},
		{"all ones", 0x1F0B10DE, 0xFFFFFFFF, false, 0},
		{"I/O BAR", 0x1F0B10DE, 0xF6000001, false, 0},
		{"64-bit BAR cannot be truncated", 0x1F0B10DE, 0xF6000004, false, 0},
		{"PCI read failure", 0x1F0B10DE, 0xF6000000, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(bdf, reg uint32) (uint32, error) {
				if reg == 0 {
					return tc.id, nil
				}
				if tc.failure {
					return 0, errors.New("read failed")
				}
				return tc.raw, nil
			}
			got, err := gpuBar0(read, 0x100)
			if got != tc.want || (err == nil) != (tc.want != 0) {
				t.Fatalf("%x, %v", got, err)
			}
		})
	}
}

func TestExclusiveRootRejectsStorageAndSwitches(t *testing.T) {
	const none = uint32(0xFFFFFFFF)
	for _, tc := range []struct {
		name   string
		mutate func(map[[2]uint32]uint32)
		want   uint32
	}{
		{"direct GPU", func(m map[[2]uint32]uint32) {}, 8},
		{"unrelated chipset SATA on bus zero", func(m map[[2]uint32]uint32) { m[[2]uint32{0xB8, 0}] = 0xA2828086; m[[2]uint32{0xB8, 8}] = 0x01060100 }, 8},
		{"NVMe on secondary bus", func(m map[[2]uint32]uint32) { m[[2]uint32{0x108, 0}] = 0x1234144D }, none},
		{"switch subtree", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x18}] = 0x00050100 }, none},
		{"not a root port", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x40}] = 0x00620010 }, none},
		{"cyclic capability list", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x40}] = 0x00004001 }, none},
		{"unaligned capability", func(m map[[2]uint32]uint32) { m[[2]uint32{8, 0x34}] = 0x41 }, none},
		{"duplicate bridge", func(m map[[2]uint32]uint32) {
			for _, reg := range []uint32{0, 8, 0xC, 0x18, 0x34, 0x40} {
				m[[2]uint32{16, reg}] = m[[2]uint32{8, reg}]
			}
		}, none},
		{"GPU disappeared", func(m map[[2]uint32]uint32) { delete(m, [2]uint32{0x100, 0}) }, none},
		{"NVIDIA audio sibling", func(m map[[2]uint32]uint32) {
			m[[2]uint32{0x100, 0xC}] = 0x00800000
			m[[2]uint32{0x101, 0}] = 0x10F910DE
			m[[2]uint32{0x101, 0xC}] = 0
			m[[2]uint32{0x101, 8}] = 0x04030000
		}, 8},
		{"storage function sibling", func(m map[[2]uint32]uint32) {
			m[[2]uint32{0x100, 0xC}] = 0x00800000
			m[[2]uint32{0x101, 0}] = 0x123410DE
			m[[2]uint32{0x101, 0xC}] = 0
			m[[2]uint32{0x101, 8}] = 0x01080000
		}, none},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := map[[2]uint32]uint32{
				{0x100, 0}: 0x1F0B10DE, {0x100, 0xC}: 0,
				{8, 0}: 0x19018086, {8, 8}: 0x06040000, {8, 0xC}: 0x00010000,
				{8, 0x18}: 0x00010100, {8, 0x34}: 0x40, {8, 0x40}: 0x00420010,
			}
			tc.mutate(m)
			var present []uint32
			for key := range m {
				if key[1] == 0 {
					present = append(present, key[0])
				}
			}
			read := func(bdf, reg uint32) (uint32, error) {
				if v, ok := m[[2]uint32{bdf, reg}]; ok {
					return v, nil
				}
				return none, nil
			}
			if got := findGPUExclusiveRoot(read, 0x100, present); got != tc.want {
				t.Fatalf("root %x, want %x", got, tc.want)
			}
			if tc.want != none {
				badRead := func(bdf, reg uint32) (uint32, error) {
					if bdf == 0x100 && reg == 0xC {
						return 0, errors.New("read error")
					}
					return read(bdf, reg)
				}
				if got := findGPUExclusiveRoot(badRead, 0x100, present); got != none {
					t.Fatal("topology read failure accepted")
				}
			}
		})
	}
}

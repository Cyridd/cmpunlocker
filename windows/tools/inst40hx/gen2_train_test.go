package main

import (
	hxcore "40hxcore"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestVerifiedPortRetrain(t *testing.T) {
	for _, name := range []string{
		"Gen2 x4", "legacy Gen2 speed vector", "stays Gen1", "no Gen2 capability", "shared topology",
		"topology changed", "TLS write ignored", "TLS IOCTL failed",
		"port request failed", "all ones after retrain", "peer stays Gen1",
		"width changed", "link still training", "restore failed",
		"topology changed during pause", "target changed externally",
	} {
		t.Run(name, func(t *testing.T) {
			const gpu, up, gc, pc = uint32(0x1100), uint32(0x1008), uint32(0x70), uint32(0x40)
			port := hxcore.PCIePort{BDF: up, VendorDevice: 0x43F41022, Capability: pc, Type: 6}
			m := map[[2]uint32]uint32{
				{gpu, gc}: 0x00020010, {up, pc}: 0x00620010,
				{gpu, gc + 12}: 0x00453D02, {up, pc + 12}: 0x00453D04,
				{gpu, gc + 0x2C}: 6, {up, pc + 0x2C}: 0x1E,
				{gpu, gc + 0x30}: 0x00008001, {up, pc + 0x30}: 0x00008004,
				{gpu, gc + 0x10}: 0x10410140, {up, pc + 0x10}: 0x30410040,
			}
			if name == "no Gen2 capability" {
				m[[2]uint32{gpu, gc + 0x2C}] = 2
			}
			if name == "legacy Gen2 speed vector" {
				m[[2]uint32{gpu, gc + 0x2C}] = 0
				m[[2]uint32{up, pc + 0x2C}] = 0
				m[[2]uint32{up, pc + 12}] = 0x00453D02
			}
			writes, requests, validations := 0, 0, 0
			read := func(b, r uint32) (uint32, error) {
				v, ok := m[[2]uint32{b, r}]
				if !ok {
					t.Fatalf("unexpected read %04x+%x", b, r)
				}
				if name == "all ones after retrain" && requests > 0 && b == gpu && r == gc+0x10 {
					return 0xFFFFFFFF, nil
				}
				return v, nil
			}
			write := func(b, r uint32, data []byte) error {
				writes++
				if len(data) != 2 || !((b == gpu && r == gc+0x30) ||
					(b == up && (r == pc+0x30 || r == pc+0x10))) {
					t.Fatalf("write outside TLS/control WORDs or GPU retrain: %04x+%x %x", b, r, data)
				}
				v := uint32(binary.LittleEndian.Uint16(data))
				old := m[[2]uint32{b, r}]
				mask := uint32(15)
				if r == pc+0x10 && b == up {
					mask = 0x20
					if v&0x20 != 0 {
						requests++
						if name == "port request failed" {
							return errors.New("request rejected")
						}
					}
				}
				if v & ^mask != (old&0xFFFF) & ^mask {
					t.Fatal("unrelated control bits modified")
				}
				if b == gpu && r == gc+0x30 && v&15 == 2 {
					if name == "TLS write ignored" {
						return nil
					}
					if name == "TLS IOCTL failed" {
						return errors.New("ambiguous TLS write failure")
					}
				}
				if name == "restore failed" && requests > 0 && b == gpu && r == gc+0x30 && v&15 == 1 {
					return errors.New("restoration failed")
				}
				m[[2]uint32{b, r}] = old&0xFFFF0000 | v
				return nil
			}
			validate := func() error {
				validations++
				if name == "shared topology" || (name == "topology changed" && validations > 1) {
					return errors.New("port is no longer verified")
				}
				if name == "topology changed during pause" && validations > 2 {
					return errors.New("port changed during pause")
				}
				return nil
			}
			pause := func(d time.Duration) {
				if d != 2200*time.Millisecond {
					return
				}
				switch name {
				case "target changed externally":
					m[[2]uint32{gpu, gc + 0x30}] = 0x8003
					m[[2]uint32{gpu, gc + 0x10}] = 0xFFFFFFFF
				case "Gen2 x4", "legacy Gen2 speed vector", "peer stays Gen1", "width changed", "link still training":
					m[[2]uint32{gpu, gc + 0x10}] = 0x10420140
					if name != "peer stays Gen1" {
						m[[2]uint32{up, pc + 0x10}] = 0x30420040
					}
					if name == "width changed" {
						m[[2]uint32{gpu, gc + 0x10}] = 0x10220140
						m[[2]uint32{up, pc + 0x10}] = 0x30220040
					}
					if name == "link still training" {
						m[[2]uint32{up, pc + 0x10}] |= 0x08000000
					}
				}
			}
			speed, err := gen2TrainVerifiedPort(gpu, gc, port, read, write, validate, pause)
			success := name == "Gen2 x4" || name == "legacy Gen2 speed vector"
			if hxcore.Gen2Reached(speed) != success || (err == nil) != (success || name == "stays Gen1") {
				t.Fatalf("speed=%d error=%v", speed, err)
			}
			if name == "no Gen2 capability" || name == "shared topology" || name == "topology changed" {
				if writes != 0 {
					t.Fatal("unverified setup issued writes")
				}
			}
			if success {
				if requests != 1 || m[[2]uint32{gpu, gc + 0x30}]&15 != 2 || m[[2]uint32{up, pc + 0x30}]&15 != 2 {
					t.Fatal("successful targets not retained")
				}
			} else if name == "topology changed during pause" {
				if requests != 0 || writes != 3 {
					t.Fatal("writes continued after topology changed")
				}
			} else if name == "target changed externally" {
				if m[[2]uint32{gpu, gc + 0x30}]&15 != 3 || m[[2]uint32{up, pc + 0x30}]&15 != 4 {
					t.Fatal("external target overwritten or unchanged peer not restored")
				}
			} else if name != "restore failed" {
				if m[[2]uint32{gpu, gc + 0x30}]&15 != 1 || m[[2]uint32{up, pc + 0x30}]&15 != 4 {
					t.Fatal("original targets not restored")
				}
			}
			if name == "stays Gen1" && requests != 6 {
				t.Fatal("retrain bound changed")
			}
		})
	}
}

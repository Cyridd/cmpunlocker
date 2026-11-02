package main

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestLinkDisableClearFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failed bool
		writes int
	}{
		{"normal", false, 2},
		{"snapshot read failure", true, 0},
		{"snapshot all ones", true, 0},
		{"already disabled", true, 0},
		{"ambiguous assertion failure", true, 2},
		{"first clear write fails", false, 3},
		{"every clear write fails", true, 3},
		{"clear writes ignored", true, 3},
		{"first clear read fails", false, 3},
		{"every clear read fails", true, 3},
		{"clear read all ones", true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const root, reg = uint32(8), uint32(0x80)
			// Nonzero status must never be replayed as a W1C DWORD write.
			const original = uint32(0xABCD0143)
			state := original
			reads, writes := 0, 0
			read := func(b, r uint32) (uint32, error) {
				if b != root || r != reg {
					t.Fatal("cycle accessed another device/register")
				}
				reads++
				switch tc.name {
				case "snapshot read failure":
					return 0, errors.New("snapshot failed")
				case "snapshot all ones":
					return 0xFFFFFFFF, nil
				case "already disabled":
					return original | 0x10, nil
				case "first clear read fails":
					if reads == 2 {
						return 0, errors.New("temporary read failure")
					}
				case "every clear read fails":
					if reads > 1 {
						return 0, errors.New("read failure")
					}
				case "clear read all ones":
					if reads > 1 {
						return 0xFFFFFFFF, nil
					}
				}
				return state, nil
			}
			write := func(b, r uint32, data []byte) error {
				if b != root || r != reg || len(data) != 2 {
					t.Fatal("cycle wrote outside the root control WORD")
				}
				writes++
				want := uint32(binary.LittleEndian.Uint16(data))
				if want&^0x10 != original&0xFFFF {
					t.Fatalf("unrelated controls changed: %04x", want)
				}
				if writes > 1 {
					if tc.name == "every clear write fails" || (tc.name == "first clear write fails" && writes == 2) {
						return errors.New("clear write failure")
					}
					if tc.name == "clear writes ignored" {
						return nil
					}
				}
				state = state&0xFFFF0000 | want
				if tc.name == "ambiguous assertion failure" && writes == 1 {
					// Simulate a submitted write whose driver nevertheless returned an error.
					return errors.New("ambiguous submission")
				}
				return nil
			}
			waitedForRetrain := false
			pause := func(d time.Duration) {
				if d == 2*time.Second {
					waitedForRetrain = true
					if state&0x10 != 0 {
						t.Fatal("continued before Link Disable clear")
					}
				}
			}
			err := gen2LinkDisableCycle(root, reg, read, write, pause)
			if (err != nil) != tc.failed || writes != tc.writes {
				t.Fatalf("got error=%v writes=%d; want failed=%v writes=%d", err, writes, tc.failed, tc.writes)
			}
			if waitedForRetrain == tc.failed {
				t.Fatal("failed/unverified cycle continued to retrain wait")
			}
			if state>>16 != original>>16 {
				t.Fatal("adjacent status was modified")
			}
		})
	}
}

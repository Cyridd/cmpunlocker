package main

// Tests for the PL0 register table.
//
// pl0V320 was recovered by disassembling a specific 3.2.0 build and is the
// sequence written into BAR0's XVE block. It is data, not logic: an accidental
// edit (a typo in an offset, a mask, or a value) compiles fine, passes vet,
// and only fails on real hardware — often as a hang before the booter runs.
// These tests make such an edit fail here instead.
//
// The expected values below are deliberately written out in full rather than
// compared against the table itself, so a change to either side is visible.

import "testing"

func TestPl0V320Table(t *testing.T) {
	type want struct {
		off  uint64
		name string
		val  uint32
		rmw  bool
		mask uint32
	}
	expected := []want{
		{0x8872C, "XVE_OVR=6", 0x00000006, false, 0x00000000},
		{0x8C040, "LINK_CONFIG_0(MAX_RATE=2)", 0x00080000, true, 0x000C0000},
		{0x8C1C0, "PL_LINK_RATE([18:17]=2)", 0x00040000, true, 0x00060000},
		{0x8841C, "PRIV_MISC_1", 0xE0B42D00, false, 0x00000000},
		{0x8C2C0, "CYA_0", 0x068731B3, false, 0x00000000},
	}

	if len(pl0V320) != len(expected) {
		t.Fatalf("pl0V320 has %d entries, want %d", len(pl0V320), len(expected))
	}
	for i, w := range expected {
		got := pl0V320[i]
		if got.off != w.off || got.name != w.name || got.val != w.val ||
			got.rmw != w.rmw || got.mask != w.mask {
			t.Errorf("pl0V320[%d] = {off:0x%X name:%q val:0x%08X rmw:%v mask:0x%08X}, "+
				"want {off:0x%X name:%q val:0x%08X rmw:%v mask:0x%08X}",
				i, got.off, got.name, got.val, got.rmw, got.mask,
				w.off, w.name, w.val, w.rmw, w.mask)
		}
	}
}

func TestPl0EntryNext(t *testing.T) {
	cases := []struct {
		name string
		e    pl0Entry
		old  uint32
		want uint32
	}{
		// Read-modify-write: only mask bits change, the rest of the register
		// keeps its current value.
		{"LINK_CONFIG_0 all mask bits clear", pl0Entry{off: regLinkConfig0, val: 0x00080000, rmw: true, mask: 0x000C0000},
			0x00000000, 0x00080000},
		{"LINK_CONFIG_0 unrelated bits kept", pl0Entry{off: regLinkConfig0, val: 0x00080000, rmw: true, mask: 0x000C0000},
			0x80085800, 0x80085800},
		{"LINK_CONFIG_0 mask bits replaced", pl0Entry{off: regLinkConfig0, val: 0x00080000, rmw: true, mask: 0x000C0000},
			0x000C0000, 0x00080000},
		{"PL_LINK_RATE mask bits replaced", pl0Entry{off: regPlLinkRate, val: 0x00040000, rmw: true, mask: 0x00060000},
			0xFFFFFFFF, 0xFFFDFFFF},
		// Plain writes: the value replaces the register wholesale.
		{"XVE_OVR constant", pl0Entry{off: regLtssmOvr, val: 0x00000006},
			0xDEADBEEF, 0x00000006},
		{"PRIV_MISC_1 constant", pl0Entry{off: regPrivMisc1, val: 0xE0B42D00},
			0x00000000, 0xE0B42D00},
		{"CYA_0 constant", pl0Entry{off: regCya0, val: 0x068731B3},
			0xFFFFFFFF, 0x068731B3},
	}
	for _, tc := range cases {
		if got := tc.e.next(tc.old); got != tc.want {
			t.Errorf("%s: next(0x%08X) = 0x%08X, want 0x%08X", tc.name, tc.old, got, tc.want)
		}
	}
}

func TestPl0EntryNextEfi(t *testing.T) {
	// nextEfi is the A/B alternative for the two registers written as
	// constants in 3.2.0: it adjusts the current value instead of overwriting.
	cases := []struct {
		name string
		e    pl0Entry
		old  uint32
		want uint32
	}{
		{"PRIV_MISC_1 sets bits 11/13, clears 12/14", pl0Entry{off: regPrivMisc1, val: 0xE0B42D00},
			0x00000000, 1<<11 | 1<<13},
		{"PRIV_MISC_1 other bits untouched", pl0Entry{off: regPrivMisc1, val: 0xE0B42D00},
			0xE0B43D00, (0xE0B43D00 | 1<<11 | 1<<13) &^ (1<<12 | 1<<14)},
		{"CYA_0 clears bit 2", pl0Entry{off: regCya0, val: 0x068731B3},
			0xFFFFFFFF, 0xFFFFFFFB},
		{"CYA_0 bit 2 already clear", pl0Entry{off: regCya0, val: 0x068731B3},
			0x068731B0, 0x068731B0},
		// Everything else falls back to the 3.2.0 behaviour.
		{"XVE_OVR uses next", pl0Entry{off: regLtssmOvr, val: 0x00000006},
			0xDEADBEEF, 0x00000006},
		{"LINK_CONFIG_0 uses next", pl0Entry{off: regLinkConfig0, val: 0x00080000, rmw: true, mask: 0x000C0000},
			0x000C0000, 0x00080000},
	}
	for _, tc := range cases {
		if got := tc.e.nextEfi(tc.old); got != tc.want {
			t.Errorf("%s: nextEfi(0x%08X) = 0x%08X, want 0x%08X", tc.name, tc.old, got, tc.want)
		}
	}
}

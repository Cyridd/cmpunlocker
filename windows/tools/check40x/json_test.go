package main

import (
	hxcore "40hxcore"
	"testing"
)

func TestTargetOnlyDoesNotProduceUnlockOK(t *testing.T) {
	for _, tc := range []struct {
		speed, target uint32
		want          string
	}{
		{1, 2, verdictComputeOnly},
		{0, 2, verdictComputeOnly},
		{15, 15, verdictComputeOnly},
		{2, 2, verdictUnlockOK},
	} {
		st := hxcore.UnlockState{SS0OK: true, Unlocked: true, Speed: tc.speed, TLS: tc.target}
		if got := statusEnumForBoot(st, true, true, false, false, true, false); got != tc.want {
			t.Errorf("speed=%d target=%d: %s, want %s", tc.speed, tc.target, got, tc.want)
		}
	}
}

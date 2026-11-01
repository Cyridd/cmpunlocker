package main

import "testing"

func TestGen2SingleInstance(t *testing.T) {
	owned, release := gen2AcquireSingleInstance()
	if !owned {
		t.Fatal("first instance did not acquire the guard")
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	for i := 0; i < 3; i++ {
		duplicate, closeDuplicate := gen2AcquireSingleInstance()
		if duplicate {
			closeDuplicate()
			t.Fatal("duplicate instance bypassed the guard")
		}
	}
	release()
	release = nil
	// A leaked duplicate handle would keep the named object alive.
	owned, nextRelease := gen2AcquireSingleInstance()
	if !owned {
		t.Fatal("guard still exists after the last instance closed")
	}
	nextRelease()
}

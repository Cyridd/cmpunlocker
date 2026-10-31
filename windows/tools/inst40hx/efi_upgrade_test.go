package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestEmbeddedEfiIsFullCpuRm(t *testing.T) {
	data, err := embedded.ReadFile("embed/" + efiFile)
	if err != nil {
		t.Fatal(err)
	}
	marker := func(text string) []byte {
		units := utf16.Encode([]rune(text))
		out := make([]byte, 2*len(units))
		for i, unit := range units {
			binary.LittleEndian.PutUint16(out[2*i:], unit)
		}
		return out
	}
	if !bytes.Contains(data, marker("CPU-RM-FULL")) || bytes.Contains(data, marker("CPU-RM-ZERO")) {
		t.Fatal("installer must embed FULL CPU-RM; ZERO is a separate diagnostic image")
	}
}

func TestEfiUpgradeDetection(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	dir := t.TempDir()
	expected := filepath.Join(dir, "selected.efi")
	deployed := filepath.Join(dir, "deployed.efi")
	data, err := embedded.ReadFile("embed/" + efiFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expected, data, 0600); err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"installer", "-efi", expected}
	if efiFileCurrent(deployed) {
		t.Fatal("missing EFI must be selected for installation")
	}
	if err := os.WriteFile(deployed, data, 0600); err != nil {
		t.Fatal(err)
	}
	if !efiFileCurrent(deployed) {
		t.Fatal("selected EFI must be recognized as current")
	}
	stale := append([]byte(nil), data...)
	stale[len(stale)/2] ^= 1
	if err := os.WriteFile(deployed, stale, 0600); err != nil {
		t.Fatal(err)
	}
	if efiFileCurrent(deployed) {
		t.Fatal("a different EFI must be selected for upgrade")
	}
	if err := os.Remove(expected); err != nil {
		t.Fatal(err)
	}
	if efiFileCurrent(deployed) {
		t.Fatal("missing explicit payload must not fall back to the embedded image")
	}
}

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// Pin the entire installer image for the local Windows packaging script.
// Rust CI independently compares the installer copy with a build from source.
func TestEmbeddedRustEfiIsFull(t *testing.T) {
	const fullSHA256 = "fdf71e56e05366cadfd3149a66c06a33a96db6642842bcc81fa6cbf4211ebf25"
	data, err := embedded.ReadFile("embed/" + efiFile)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != fullSHA256 {
		t.Fatalf("embedded EFI SHA-256 = %s, want reviewed Rust FULL %s", got, fullSHA256)
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

package hxcore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestVerifiedFileReplacement(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bootx64.efi")
	old, next := []byte("old boot image"), []byte("new boot image")
	if err := os.WriteFile(p, old, 0600); err != nil {
		t.Fatal(err)
	}
	for _, fail := range []string{"read", "same-size corruption", "rename"} {
		t.Run(fail, func(t *testing.T) {
			read := func(tmp string) ([]byte, error) {
				if fail == "read" {
					return nil, errors.New("injected read failure")
				}
				b, err := os.ReadFile(tmp)
				if fail == "same-size corruption" && err == nil {
					b[0] ^= 1
				}
				return b, err
			}
			move := func(from, to string, replace bool) error {
				if fail == "rename" {
					return errors.New("injected rename failure")
				}
				t.Fatal("an unverified image reached the live name")
				return nil
			}
			if err := stageFileVerified(p, next, 0600, true, read, move); err == nil {
				t.Fatal("failure accepted")
			}
			if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, old) {
				t.Fatalf("old image lost: %q, %v", got, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("staging file leaked: %v, %v", entries, err)
			}
		})
	}
	if err := WriteFileVerified(p, next, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, next) {
		t.Fatalf("replacement: %q, %v", got, err)
	}
}

func TestVerifiedReplacementSharingViolation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bootx64.efi")
	old := []byte("existing boot image")
	if err := os.WriteFile(p, old, 0600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err := WriteFileVerified(p, []byte("replacement"), 0600); err == nil {
		t.Fatal("locked destination was replaced")
	}
	if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, old) {
		t.Fatalf("locked original changed: %q, %v", got, err)
	}
}

func TestBackupNeverMovesSourceOrOverwritesBackup(t *testing.T) {
	dir := t.TempDir()
	src, bak := filepath.Join(dir, "bootx64.efi"), filepath.Join(dir, "bootx64.efi.40hx.bak")
	old := []byte("original fallback")
	if err := os.WriteFile(src, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := BackupFileVerified(src, bak); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{src, bak} {
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, old) {
			t.Fatalf("%s: %q, %v", p, got, err)
		}
	}
	if err := os.WriteFile(src, []byte("later unlock"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BackupFileVerified(src, bak); err == nil {
		t.Fatal("existing backup overwritten")
	}
	if got, err := os.ReadFile(bak); err != nil || !bytes.Equal(got, old) {
		t.Fatalf("original backup lost: %q, %v", got, err)
	}
	if err := WriteFileVerified(src, old, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(bak); err != nil || !bytes.Equal(got, old) {
		t.Fatalf("restore consumed backup: %q, %v", got, err)
	}
}

func TestRestoreKeepsBackupOnSameSizeCorruption(t *testing.T) {
	dir := t.TempDir()
	live, bak := filepath.Join(dir, "bootx64.efi"), filepath.Join(dir, "bootx64.efi.40hx.bak")
	data := []byte("original image")
	if err := os.WriteFile(bak, data, 0600); err != nil {
		t.Fatal(err)
	}
	read := func(p string) ([]byte, error) {
		b, err := os.ReadFile(p)
		if p == live && err == nil {
			b[0] ^= 1
		}
		return b, err
	}
	if err := restoreFileBackupVerified(live, bak, read, WriteFileVerified, os.Remove); err == nil {
		t.Fatal("same-size corruption accepted")
	}
	if got, err := os.ReadFile(bak); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("backup lost: %q, %v", got, err)
	}
	if err := RestoreFileBackupVerified(live, bak); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatalf("verified backup cleanup: %v", err)
	}
	if got, err := os.ReadFile(live); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("restore: %q, %v", got, err)
	}
}

func TestEmptyBackupIsRefused(t *testing.T) {
	dir := t.TempDir()
	live, bak := filepath.Join(dir, "bootx64.efi"), filepath.Join(dir, "bootx64.efi.40hx.bak")
	if err := os.WriteFile(live, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := BackupFileVerified(live, bak); err == nil {
		t.Fatal("empty source accepted as a boot backup")
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatalf("empty backup was created: %v", err)
	}
	old := []byte("working boot image")
	if err := os.WriteFile(live, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bak, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreFileBackupVerified(live, bak); err == nil {
		t.Fatal("empty backup accepted for restore")
	}
	if got, err := os.ReadFile(live); err != nil || !bytes.Equal(got, old) {
		t.Fatalf("empty restore changed working image: %q, %v", got, err)
	}
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("failed restore consumed backup: %v", err)
	}
}

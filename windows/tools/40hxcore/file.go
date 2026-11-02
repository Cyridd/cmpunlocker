package hxcore

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// WriteFileVerified stages and flushes a complete file in the destination
// directory before replacing the live name. A failed stage or rename leaves
// the previous destination intact. This is not a power-loss transaction for
// the whole ESP, nor a guarantee against faulty storage hardware.
func WriteFileVerified(path string, data []byte, mode os.FileMode) error {
	return stageFileVerified(path, data, mode, true, os.ReadFile, moveStagedFile)
}

// BackupFileVerified never moves the live source or replaces an older backup.
func BackupFileVerified(source, backup string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("refusing empty boot backup source %s", source)
	}
	return stageFileVerified(backup, data, 0600, false, os.ReadFile, moveStagedFile)
}

// RestoreFileBackupVerified consumes the backup only after exact readback.
func RestoreFileBackupVerified(path, backup string) error {
	return restoreFileBackupVerified(path, backup, os.ReadFile, WriteFileVerified, os.Remove)
}

func restoreFileBackupVerified(path, backup string, read func(string) ([]byte, error),
	write func(string, []byte, os.FileMode) error, remove func(string) error) error {
	data, err := read(backup)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("refusing empty boot backup %s", backup)
	}
	if err := write(path, data, 0644); err != nil {
		return err
	}
	rb, err := read(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(rb, data) {
		return fmt.Errorf("restored %s differs from backup; backup retained", path)
	}
	return remove(backup)
}

func moveStagedFile(from, to string, replace bool) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	// Deliberately omit COPY_ALLOWED: the stage must stay on the same volume.
	return windows.MoveFileEx(f, t, flags)
}

func stageFileVerified(path string, data []byte, mode os.FileMode, replace bool,
	read func(string) ([]byte, error), move func(string, string, bool) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".40hx-stage-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		var n int
		n, err = f.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	rb, err := read(tmp)
	if err != nil {
		return fmt.Errorf("verify staged %s: %w", path, err)
	}
	if !bytes.Equal(rb, data) {
		return fmt.Errorf("verify staged %s: content mismatch", path)
	}
	return move(tmp, path, replace)
}

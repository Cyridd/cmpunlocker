// SPDX-License-Identifier: MIT
package hxcore

import (
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

const efiLogBudget = 8 << 20

var (
	logFilename  = regexp.MustCompile(`^L([0-9]{7})\.LOG$`)
	logHeader    = regexp.MustCompile(`^(CMPUNLOCKER|CMP40)_LOG_V3[\t\r ]+generation=([0-9]{7})[\t\r ]+control=(full|zero|logcheck|entrycheck)[\t\r ]*$`)
	logFooter    = regexp.MustCompile(`^(CMPUNLOCKER|CMP40)_END_V3 kind=[ST] bytes=([0-9]{20}) fnv=([0-9a-fA-F]{16})\n$`)
	errEfiAbsent = errors.New("unlock EFI is absent from the ESP")
	errNoEfiLog  = errors.New("no EFI log on the ESP")
)

type efiLog struct {
	name, prefix, control string
	data                  []byte
}

// Hold each directory open without delete sharing so it cannot be replaced
// while a child is being inspected. Inspect reparse points without following them.
func openLogNode(path string, directory, remove bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	if remove {
		access |= windows.DELETE
	}
	share := uint32(windows.FILE_SHARE_READ)
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
		share |= windows.FILE_SHARE_WRITE
	}
	h, err := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		file.Close()
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		(info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		file.Close()
		return nil, fmt.Errorf("unexpected EFI log file type: %s", path)
	}
	return file, nil
}

// Mark the same open object whose header was checked for deletion on close.
func deleteLogNode(file *os.File) error {
	flag := byte(1)
	return windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileDispositionInfo, &flag, 1)
}

type logScanMode int

const (
	inspectLatestLog logScanMode = iota
	removeOwnedLogs
)

// Inspect the newest matching generation, or remove all owned records on uninstall.
func visitManagedLogs(esp string, mode logScanMode, visit func(efiLog, *os.File) error) (int, error) {
	cleanup := mode == removeOwnedLogs
	path := esp
	var dir *os.File
	for _, part := range []string{"EFI", "40HX", "LOGS"} {
		path = filepath.Join(path, part)
		var err error
		dir, err = openLogNode(path, true, cleanup && part == "LOGS")
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		defer dir.Close()
	}
	entries, err := dir.ReadDir(4097)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if len(entries) > 4096 {
		return 0, fmt.Errorf("EFI log directory exceeds 4096 entries")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		gen := logFilename.FindStringSubmatch(name)
		if !entry.Type().IsRegular() || gen == nil || gen[1] == "0000000" {
			continue
		}
		file, err := openLogNode(filepath.Join(path, name), false, cleanup)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return found, err
		}
		var buf [96]byte
		n, err := file.ReadAt(buf[:], 0)
		if err != nil && !errors.Is(err, io.EOF) {
			file.Close()
			return found, err
		}
		line, _, ok := bytes.Cut(buf[:n], []byte{'\n'})
		header := logHeader.FindSubmatch(line)
		if !ok || header == nil || string(header[2]) != gen[1] {
			file.Close()
			continue
		}
		err = visit(efiLog{name: name, prefix: string(header[1]), control: string(header[3])}, file)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return found, errors.Join(err, closeErr)
		}
		found++
		if !cleanup {
			break
		}
	}
	if cleanup && len(entries) == found {
		if err := deleteLogNode(dir); err != nil && !errors.Is(err, windows.ERROR_DIR_NOT_EMPTY) {
			return found, err
		}
	}
	return found, nil
}

func removeManagedLogs(esp string) (int, error) {
	return visitManagedLogs(esp, removeOwnedLogs, func(_ efiLog, file *os.File) error {
		return deleteLogNode(file)
	})
}

func readEfiLog(esp string) (efiLog, error) {
	var log efiLog
	_, err := os.Stat(filepath.Join(esp, "EFI", "40HX", "40HXUNLK.EFI"))
	if errors.Is(err, os.ErrNotExist) {
		return log, errEfiAbsent
	}
	if err != nil {
		return log, err
	}
	found, err := visitManagedLogs(esp, inspectLatestLog, func(record efiLog, file *os.File) error {
		data, err := io.ReadAll(io.LimitReader(file, efiLogBudget+1))
		if err != nil {
			return err
		}
		if len(data) > efiLogBudget {
			return fmt.Errorf("%s exceeds the EFI log budget", record.name)
		}
		log = record
		log.data = data
		return nil
	})
	if found != 0 || err != nil {
		return log, err
	}
	log.name = "40hx_log.txt"
	file, err := openLogNode(filepath.Join(esp, log.name), false, false)
	if errors.Is(err, os.ErrNotExist) {
		return log, errNoEfiLog
	}
	if err != nil {
		return log, err
	}
	defer file.Close()
	log.data, err = io.ReadAll(file)
	return log, err
}

func (log efiLog) verifiedBody() ([]byte, bool) {
	if len(log.data) == 0 || log.data[len(log.data)-1] != '\n' {
		return nil, false
	}
	start := bytes.LastIndexByte(log.data[:len(log.data)-1], '\n') + 1
	footer := logFooter.FindSubmatch(log.data[start:])
	if footer == nil || string(footer[1]) != log.prefix {
		return nil, false
	}
	size, err := strconv.ParseUint(string(footer[2]), 10, 64)
	if err != nil || size != uint64(start) {
		return nil, false
	}
	body := log.data[:start]
	sum := fnv.New64a()
	sum.Write(body)
	want, err := strconv.ParseUint(string(footer[3]), 16, 64)
	return body, err == nil && want == sum.Sum64()
}

func (log efiLog) diagnosis() string {
	label := fmt.Sprintf("[EFI %s, %s] ", log.name, log.control)
	body, ok := log.verifiedBody()
	if !ok {
		return label + T("Incomplete log or invalid checksum.", "Журнал не завершён или не прошёл проверку контрольной суммы.", "日志不完整或校验失败。")
	}
	prefix := "CMPUNLOCKER: "
	if log.prefix == "CMP40" {
		prefix = "CMP40-CPU-RM: "
	}
	handoff := false
	for line := range strings.SplitSeq(string(body), "\n") {
		event, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		if strings.HasPrefix(event, "ABORT ") {
			return label + T("Verified log records ABORT.", "Проверенный журнал содержит ABORT.", "已校验的日志记录了 ABORT。")
		}
		if log.control == "full" && event == "HANDOFF_READY Full" || log.control == "zero" && event == "HANDOFF_READY Zero" {
			handoff = true
		}
	}
	if handoff {
		return label + T("Verified HANDOFF_READY before OS handoff.", "Подтверждена отметка HANDOFF_READY перед передачей управления ОС.", "日志已校验，记录了移交操作系统前的 HANDOFF_READY。")
	}
	return label + T("Log verified.", "Целостность журнала подтверждена.", "日志已校验。")
}

// CollectEfiLog copies the latest record verbatim, including interrupted runs.
func CollectEfiLog(esp, destination string) (string, error) {
	log, err := readEfiLog(esp)
	if err != nil {
		return "", err
	}
	return log.name, WriteFileVerified(filepath.Join(destination, log.name), log.data, 0644)
}

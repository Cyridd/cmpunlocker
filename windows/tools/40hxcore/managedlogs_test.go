// SPDX-License-Identifier: MIT
package hxcore

import (
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func logFixture(body, prefix, kind string) string {
	sum := fnv.New64a()
	sum.Write([]byte(body))
	return fmt.Sprintf("%s%s_END_V3 kind=%s bytes=%020d fnv=%016x\n", body, prefix, kind, len(body), sum.Sum64())
}

func putLogFile(t *testing.T, root, name, data string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManagedLogDiagnosis(t *testing.T) {
	for _, tc := range []struct{ prefix, control, tag, event, kind, want string }{
		{"CMPUNLOCKER", "full", "CMPUNLOCKER", "HANDOFF_READY Full", "S", "before OS handoff"},
		{"CMP40", "zero", "CMP40-CPU-RM", "HANDOFF_READY Zero", "T", "before OS handoff"},
		{"CMPUNLOCKER", "full", "CMPUNLOCKER", "HANDOFF_READY Zero", "S", "Log verified."},
		{"CMPUNLOCKER", "full", "CMPUNLOCKER", "ABORT test", "T", "records ABORT"},
		{"CMPUNLOCKER", "entrycheck", "CMPUNLOCKER-ENTRYCHECK", "ENTRY_CHECK_READY", "S", "Log verified."},
		{"CMP40", "logcheck", "CMP40-LOGCHECK", "LOG_CHECK_READY", "S", "Log verified."},
	} {
		body := fmt.Sprintf("%s_LOG_V3 generation=0000001 control=%s\n%s: %s\n", tc.prefix, tc.control, tc.tag, tc.event)
		record := logFixture(body, tc.prefix, tc.kind)
		log := efiLog{name: "L0000001.LOG", prefix: tc.prefix, control: tc.control, data: []byte(record)}
		if got := log.diagnosis(); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %s", tc.event, got)
		}
		for _, corrupt := range []string{body, record[:len(record)-1], strings.Replace(record, "bytes=0", "bytes=1", 1), strings.Replace(record, "fnv=", "fnv=f", 1), strings.Replace(record, "generation=", "Generation=", 1), record + "garbage\n"} {
			log.data = []byte(corrupt)
			if got := log.diagnosis(); !strings.Contains(got, "Incomplete") {
				t.Errorf("%s: accepted corrupt log: %s", tc.control, got)
			}
		}
	}
}

func TestEFIExportAndCleanup(t *testing.T) {
	esp, out := t.TempDir(), t.TempDir()
	legacy := putLogFile(t, esp, "40hx_log.txt", "legacy log\n")
	if _, err := CollectEfiLog(esp, out); !errors.Is(err, errEfiAbsent) {
		t.Fatalf("undeployed: %v", err)
	}
	putLogFile(t, esp, "EFI/40HX/40HXUNLK.EFI", "test fixture")
	checkExport := func(name, want string) {
		t.Helper()
		got, err := CollectEfiLog(esp, out)
		if err != nil || got != name {
			t.Fatalf("export: %q, %v", got, err)
		}
		data, err := os.ReadFile(filepath.Join(out, got))
		if err != nil || !bytes.Equal(data, []byte(want)) {
			t.Fatalf("exported bytes differ for %s: %v", got, err)
		}
	}
	checkExport("40hx_log.txt", "legacy log\n")
	if got := analyzeEfiLogOnESP(esp); !strings.Contains(got, "Legacy EFI log") {
		t.Fatal(got)
	}

	const dir = "EFI/40HX/LOGS/"
	summary := logFixture("CMP40_LOG_V3 generation=0000999 control=zero\nCMP40-CPU-RM: HANDOFF_READY Zero\n", "CMP40", "S")
	putLogFile(t, esp, dir+"L0000999.LOG", summary)
	checkExport("L0000999.LOG", summary)
	if got := analyzeEfiLogOnESP(esp); !strings.Contains(got, "before OS handoff") {
		t.Fatal(got)
	}
	partial := "CMPUNLOCKER_LOG_V3 generation=0001000 control=full\nCMPUNLOCKER: HANDOFF_READY Full\n"
	putLogFile(t, esp, dir+"L0001000.LOG", partial)
	foreign := []string{legacy}
	for name, data := range map[string]string{
		dir + "L0001001.LOG": "CMPUNLOCKER_LOG_V3 generation=0000999 control=full\n",
		dir + "L0001002.LOG": "CMPUNLOCKER_LOG_V3 generation=0001002 control=other\n",
		dir + "L0001003.LOG": "unrelated\n",
		dir + "L0000000.LOG": "CMP40_LOG_V3 generation=0000000 control=full\n",
		dir + "OTHER.LOG":    partial,
		"RF000001.LOG":       partial,
	} {
		foreign = append(foreign, putLogFile(t, esp, name, data))
	}
	checkExport("L0001000.LOG", partial)
	if got := analyzeEfiLogOnESP(esp); !strings.Contains(got, "L0001000.LOG") || !strings.Contains(got, "Incomplete") {
		t.Fatal(got)
	}
	count, err := removeManagedLogs(esp)
	if err != nil || count != 2 {
		t.Fatalf("removed %d: %v", count, err)
	}
	for _, path := range foreign {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	checkExport("40hx_log.txt", "legacy log\n")
	count, err = removeManagedLogs(esp)
	if err != nil || count != 0 {
		t.Fatalf("second cleanup removed %d: %v", count, err)
	}
}

func TestManagedLogErrorsAndEmptyDirectory(t *testing.T) {
	esp := t.TempDir()
	putLogFile(t, esp, "EFI/40HX/40HXUNLK.EFI", "test fixture")
	if _, err := readEfiLog(esp); !errors.Is(err, errNoEfiLog) {
		t.Fatalf("missing log: %v", err)
	}
	path := putLogFile(t, esp, "EFI/40HX/LOGS/L0000001.LOG", "CMPUNLOCKER_LOG_V3 generation=0000001 control=full\n")
	if err := os.Truncate(path, efiLogBudget+1); err != nil {
		t.Fatal(err)
	}
	if _, err := readEfiLog(esp); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized log: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	count, cleanupErr := removeManagedLogs(esp)
	file.Close()
	if cleanupErr == nil || count != 0 {
		t.Fatalf("cleanup of file in use: %d, %v", count, cleanupErr)
	}
	count, err = removeManagedLogs(esp)
	if err != nil || count != 1 {
		t.Fatalf("cleanup after close: %d, %v", count, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty directory: %v", err)
	}
	if count, err := removeManagedLogs(esp); err != nil || count != 0 {
		t.Fatalf("missing directory: %d, %v", count, err)
	}
}

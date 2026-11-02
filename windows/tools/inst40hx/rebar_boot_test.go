package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRebarBootOption(t *testing.T) {
	const guid = "d12b885f-3803-11f1-9145-9a610daa7b77"
	for _, enable := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "enable"}[enable], func(t *testing.T) {
			value := "norebar"
			if enable {
				value = rebarEnableLoadOptions
			}
			calls := 0
			run := func(name string, args ...string) (string, error) {
				calls++
				if name != "bcdedit.exe" {
					t.Fatalf("unexpected command %q", name)
				}
				want := []string{"/set", "{" + guid + "}", "loadoptions", value}
				if calls == 2 {
					want = []string{"/enum", "{" + guid + "}"}
				}
				if !reflect.DeepEqual(args, want) || calls > 2 {
					t.Fatalf("call %d: got %q, want %q", calls, args, want)
				}
				if calls == 1 {
					// Guard against BCDEdit interpreting the value as an option.
					if strings.HasPrefix(args[3], "-") || strings.HasPrefix(args[3], "/") {
						return "The parameter is incorrect.", errors.New("exit status 1")
					}
					return "The operation completed successfully.", nil
				}
				return "identifier              {" + guid + "}\r\nloadoptions\t" + value + "\r\n", nil
			}
			if err := setRebarBootOption(run, guid, enable); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("readback not performed: %d calls", calls)
			}
		})
	}
}

func TestRebarBootOptionFailures(t *testing.T) {
	sentinel := errors.New("exit status 1")
	for _, tc := range []struct {
		name, readback, output string
		failAt                 int
	}{
		{name: "set rejected", failAt: 1, output: "The parameter is incorrect."},
		{name: "readback rejected", failAt: 2, output: "Access is denied."},
		{name: "old norebar retained", readback: "loadoptions norebar\r\n"},
		{name: "conflicting opt out", readback: "loadoptions " + rebarEnableLoadOptions + " norebar\n"},
		{name: "uppercased option", readback: "loadoptions " + strings.ToUpper(rebarEnableLoadOptions) + "\n"},
		{name: "missing option", readback: "description 40HX Unlock\n"},
		{name: "empty option", readback: "loadoptions\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			run := func(string, ...string) (string, error) {
				calls++
				if calls == tc.failAt {
					return tc.output, sentinel
				}
				return tc.readback, nil
			}
			err := setRebarBootOption(run, "d12b885f-3803-11f1-9145-9a610daa7b77", true)
			if err == nil {
				t.Fatal("failed or unverified write reported success")
			}
			if tc.failAt > 0 {
				if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), tc.output) {
					t.Fatalf("BCDEdit error/output lost: %v", err)
				}
				if calls != tc.failAt {
					t.Fatalf("continued after failure: %d calls", calls)
				}
			}
		})
	}
}

func TestExistingBootEntryPreserved(t *testing.T) {
	const guid = "d12b885f-3803-11f1-9145-9a610daa7b77"
	calls := 0
	run := func(name string, args ...string) (string, error) {
		calls++
		if name != "bcdedit.exe" {
			t.Fatalf("unexpected command %q", name)
		}
		if calls != 1 || !reflect.DeepEqual(args, []string{"/enum", "firmware"}) {
			t.Fatalf("existing boot configuration was modified: %q", args)
		}
		return "Firmware Boot Manager\r\nidentifier {fwbootmgr}\r\ndisplayorder {11111111-1111-1111-1111-111111111111}\r\n             {" + guid + "}\r\n\r\nFirmware Application\r\n--------------------\r\nidentifier {" + guid + "}\r\npath \\EFI\\40HX\\40HXUNLK.EFI\r\ndescription 40HX Unlock\r\nloadoptions " + rebarEnableLoadOptions + "\r\n", nil
	}
	if err := setupBootEntryWithRunner(run, nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("existing entry should only be enumerated: %d calls", calls)
	}
}

func TestBootEntryEnumerationFailure(t *testing.T) {
	sentinel := errors.New("exit status 1")
	calls := 0
	run := func(string, ...string) (string, error) {
		calls++
		return "Access is denied.", sentinel
	}
	err := setupBootEntryWithRunner(run, nil, nil)
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "Access is denied.") || calls != 1 {
		t.Fatalf("failure was lost or execution continued: calls=%d err=%v", calls, err)
	}
}

func TestNewBootEntryAppended(t *testing.T) {
	const guid = "d12b885f-3803-11f1-9145-9a610daa7b77"
	entry := "{" + guid + "}"
	for _, failAt := range []int{0, 5} {
		t.Run(map[int]string{0: "success", 5: "registration rejected"}[failAt], func(t *testing.T) {
			sentinel := errors.New("registration rejected")
			var calls [][]string
			run := func(name string, args ...string) (string, error) {
				if name != "bcdedit.exe" {
					t.Fatalf("unexpected command %q", name)
				}
				calls = append(calls, append([]string(nil), args...))
				if len(calls) == failAt {
					return "Access is denied.", sentinel
				}
				if args[0] == "/copy" {
					return "The entry was successfully copied to " + entry, nil
				}
				return "", nil
			}
			mounted, unmounted := 0, 0
			err := setupBootEntryWithRunner(run, func() string {
				mounted++
				return "S"
			}, func(esp string) {
				if esp != "S" {
					t.Fatalf("unexpected ESP %q", esp)
				}
				unmounted++
			})
			want := [][]string{
				{"/enum", "firmware"},
				{"/copy", "{bootmgr}", "/d", bootDesc},
				{"/set", entry, "device", "partition=S:"},
				{"/set", entry, "path", "\\EFI\\40HX\\40HXUNLK.EFI"},
				{"/set", "{fwbootmgr}", "displayorder", entry, "/addlast"},
			}
			if failAt != 0 {
				want = append(want, []string{"/delete", entry, "/f"})
				if !errors.Is(err, sentinel) {
					t.Fatalf("registration failure was lost: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("new entry was promoted or cleanup was incorrect: got %q, want %q", calls, want)
			}
			if mounted != 1 || unmounted != 1 {
				t.Fatalf("ESP cleanup mismatch: mounts=%d unmounts=%d", mounted, unmounted)
			}
		})
	}
}

func TestEmbeddedEfiSupportsEspRebarPolicy(t *testing.T) {
	data, err := embedded.ReadFile("embed/" + efiFile)
	if err != nil {
		t.Fatal(err)
	}
	if !rebarPolicySupported(data) {
		t.Fatal("installer must embed the EFI that reads the ESP ReBAR policy")
	}
	if rebarPolicySupported([]byte("old EFI without ESP policy support")) {
		t.Fatal("old EFI accepted for the new opt-in transport")
	}
}

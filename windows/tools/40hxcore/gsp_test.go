package hxcore

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// Use an isolated HKCU fixture, never the host's GPU or HKLM registry.
func TestDisableGspUpgrade(t *testing.T) {
	path := fmt.Sprintf(`Software\CMP40HX-Test-%d`, time.Now().UnixNano())
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	defer key.Close()
	if err := key.SetStringValue("UnrelatedSetting", "preserved"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"missing", "enabled", "disabled"} {
		t.Run(state, func(t *testing.T) {
			if state == "missing" {
				key.DeleteValue(GpuEnableFw)
			} else {
				value := uint32(0)
				if state == "enabled" {
					value = 1
				}
				if err := key.SetDWordValue(GpuEnableFw, value); err != nil {
					t.Fatal(err)
				}
			}
			if gspDisabledAt(registry.CURRENT_USER, path) != (state == "disabled") {
				t.Fatal("status must require explicit zero")
			}
			if err := disableGspAt(registry.CURRENT_USER, path); err != nil {
				t.Fatal(err)
			}
			value, kind, err := key.GetIntegerValue(GpuEnableFw)
			if err != nil || kind != registry.DWORD || value != 0 || !gspDisabledAt(registry.CURRENT_USER, path) {
				t.Fatalf("upgrade: value=%d type=%d err=%v", value, kind, err)
			}
			if other, _, err := key.GetStringValue("UnrelatedSetting"); err != nil || other != "preserved" {
				t.Fatal("unrelated registry value changed")
			}
		})
	}
	if err := disableGspAt(registry.CURRENT_USER, path+`\MissingAdapter`); err == nil {
		t.Fatal("must refuse a missing adapter key instead of creating one")
	}
}

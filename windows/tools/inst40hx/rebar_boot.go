package main

import (
	"bytes"
	"fmt"
	"strings"
)

// Avoid a leading '-' in BCDEdit's value, which can be parsed as an option.
// An image-name prefix keeps this a data argument; the shipped EFI scans all whitespace-delimited
// tokens, so it still recognizes --experimental-rebar without an EFI upgrade.
const rebarEnableLoadOptions = "40HXUNLK.EFI --experimental-rebar"

// Firmware may pass the copied boot manager's binary OptionalData instead of
// BCD loadoptions. The EFI also reads this explicit policy on its own ESP.
const rebarPolicyEnabled = "CMP40HX_REBAR_V1=1\n"
const rebarPolicyDisabled = "CMP40HX_REBAR_V1=0\n"

func rebarPolicySupported(image []byte) bool {
	return bytes.Contains(image, []byte(rebarPolicyEnabled))
}

type bcdCommandFunc func(string, ...string) (string, error)

func setRebarBootOption(run bcdCommandFunc, guid string, enable bool) error {
	value := "norebar"
	if enable {
		value = rebarEnableLoadOptions
	}
	entry := "{" + guid + "}"
	out, err := run("bcdedit.exe", "/set", entry, "loadoptions", value)
	if err != nil {
		return fmt.Errorf("bcdedit /set %s loadoptions: %w\n%s", entry, err, strings.TrimSpace(out))
	}
	out, err = run("bcdedit.exe", "/enum", entry)
	if err != nil {
		return fmt.Errorf("bcdedit /enum %s (readback): %w\n%s", entry, err, strings.TrimSpace(out))
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.EqualFold(fields[0], "loadoptions") {
			actual := strings.Join(fields[1:], " ")
			if actual == value {
				return nil
			}
			return fmt.Errorf("boot entry %s loadoptions readback: got %q, want %q", entry, actual, value)
		}
	}
	return fmt.Errorf("boot entry %s: no loadoptions in BCDEdit readback\n%s", entry, strings.TrimSpace(out))
}

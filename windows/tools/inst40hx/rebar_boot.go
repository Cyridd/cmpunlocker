package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
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
	if bytes.Contains(image, []byte(rebarPolicyEnabled)) {
		return true // C EFI embeds the literal policy value.
	}
	// Optimized Rust builds can inline the value comparison. The file reader
	// embeds the UEFI path (NUL-terminated CHAR16) and its policy diagnostic.
	var path []byte
	for _, c := range efiDir + "\\" + rebarPolicyFile + "\x00" {
		path = binary.LittleEndian.AppendUint16(path, uint16(c))
	}
	return bytes.Contains(image, path) &&
		(bytes.Contains(image, []byte("CMPUNLOCKER-REBAR: POLICY file=")) ||
			bytes.Contains(image, []byte("CMP40-REBAR: POLICY file=")))
}

func verifyRebarImages(selected []byte, primary, fallback string) error {
	if !rebarPolicySupported(selected) {
		return errors.New(tr("the selected EFI has no recognized ESP ReBAR policy reader",
			"в выбранном EFI не распознан обработчик файла REBAR.CFG", "未识别到所选 EFI 的 REBAR.CFG 读取功能"))
	}
	for _, path := range []string{primary, fallback} {
		image, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf(tr("cannot read EFI %s: %w", "не удалось прочитать EFI %s: %w", "无法读取 EFI %s: %w"), path, err)
		}
		if !bytes.Equal(image, selected) {
			return fmt.Errorf(tr("EFI at %s differs from the selected image",
				"EFI по пути %s отличается от выбранного образа", "%s 处的 EFI 与所选映像不一致"), path)
		}
	}
	return nil
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

// 40HXCheck -json: 机器可读诊断输出
//
// 设计约束:
//
//  1. 字段名与枚举值一律英文、固定 —— 它们是被脚本/工单系统消费的接口。人类可读
//     的文本(建议、结论句)才本地化。若把语言写进 JSON, 下游解析会随 -lang 漂移,
//     所以这里刻意不调用 tr() 生成任何字段名或状态枚举。
//  2. 数值保留原始十六进制/十进制值, 不预格式化成 "8 GB" 之类的展示串 —— 需要
//     展示串的调用方自己格式化, 需要比较的调用方直接用数值。
//  3. 未知/读不到的项用指针 + omitempty 表达, 与"实测为 0"区分开。这是本工具最
//     容易出错的地方: SS0=0x00000000(真锁定) 和"没读到"必须能分辨。
//
// 用法: 40HXCheck.exe -json      (需管理员, 与文本模式相同)
//
// 输出: stdout 为单个 JSON 文档(不混入任何其他行); 完整文本诊断仍写入 logs 目录。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	hxcore "40hxcore"
)

// jsonReport 是 -json 模式的顶层对象。
type jsonReport struct {
	Schema    string       `json:"schema"`
	Version   string       `json:"version"`
	Timestamp string       `json:"timestamp"`
	Admin     bool         `json:"administrator"`
	OS        string       `json:"os"`
	GPU       jsonGPU      `json:"gpu"`
	Firmware  jsonFirmware `json:"firmware"`
	Compute   jsonCompute  `json:"compute"`
	PCIe      jsonPCIe     `json:"pcie"`
	ReBAR     jsonReBAR    `json:"rebar"`
	Verdict   jsonVerdict  `json:"verdict"`
	Notes     []string     `json:"notes,omitempty"`
}

type jsonGPU struct {
	Present40HX bool    `json:"cmp40hx_present"`
	GSP         string  `json:"gsp"`
	BDF         *string `json:"bdf,omitempty"`
}

type jsonFirmware struct {
	SecureBoot       bool   `json:"secure_boot"`
	BootMode         string `json:"boot_mode"` // "uefi" | "legacy"
	TestSigning      bool   `json:"test_signing"`
	FastStartup      bool   `json:"fast_startup"`
	UnlockEFI        string `json:"unlock_efi_state"` // "unsigned" | "signed" | "not_deployed" | "unreadable"
	UnlockEFISigner  string `json:"unlock_efi_signer,omitempty"`
	UnlockEFISelfSig bool   `json:"unlock_efi_self_signed,omitempty"`
}

type jsonCompute struct {
	Measured bool    `json:"measured"`
	Unlocked bool    `json:"unlocked"`
	SS0      *uint32 `json:"ss0,omitempty"`
	SS1      *uint32 `json:"ss1,omitempty"`
}

type jsonPCIe struct {
	Measured    bool   `json:"measured"`
	LinkSpeed   uint32 `json:"link_speed_gen,omitempty"`
	LinkWidth   uint32 `json:"link_width,omitempty"`
	TargetSpeed uint32 `json:"target_speed_gen,omitempty"`
	ASPM        string `json:"aspm"` // "off" | "on" | "unknown"
	ASPMAC      uint32 `json:"aspm_ac,omitempty"`
	ASPMDC      uint32 `json:"aspm_dc,omitempty"`
}

type jsonReBAR struct {
	Measured bool   `json:"measured"`
	Enabled  bool   `json:"enabled"`
	BAR1MiB  uint32 `json:"bar1_mib,omitempty"`
}

type jsonVerdict struct {
	Status  string `json:"status"`
	Compute string `json:"compute"`
	PCIe    string `json:"pcie"`
}

// verdictStatus 是一组固定枚举; README 里对它们有说明, 下游按此分支。
const (
	verdictUnlockOK        = "unlock_ok"
	verdictComputeOnly     = "compute_only"
	verdictNotUnlocked     = "not_unlocked"
	verdictDriverNotLoaded = "driver_not_loaded"
	verdictLegacyBoot      = "legacy_boot_no_efi"
	verdictSecureBootBlock = "secure_boot_blocks_unsigned_efi"
	verdictInconclusive    = "inconclusive"
)

// statusEnum 把文本模式那个大 switch 的判定结果映射到固定枚举。
// 单独抽出来是为了让文本与 JSON 两条路径永远给出同一结论 —— 判定逻辑只有这一份。
func statusEnum(st hxcore.UnlockState, drvOK, gpuOK, sbOn, efiSigned bool, efiDeployed bool) string {
	switch {
	case hxcore.FirmwareIsLegacy():
		return verdictLegacyBoot
	// Secure Boot 开着 + 部署的 EFI 未签名 ⇒ 固件根本不会执行它, 此时报"未解锁"
	// 会掩盖真正原因, 所以单列一类。
	case sbOn && (!efiDeployed || !efiSigned):
		return verdictSecureBootBlock
	case st.Unlocked && st.SS0OK && (st.Speed >= 2 || st.TLS >= 2):
		return verdictUnlockOK
	case st.Unlocked && st.SS0OK:
		return verdictComputeOnly
	case st.SS0OK:
		return verdictNotUnlocked
	case !drvOK:
		return verdictDriverNotLoaded
	default:
		return verdictInconclusive
	}
}

// efiSignatureFacts 汇总"ESP 上那个 EFI 的签名状态", 供文本与 JSON 共用。
// 只挂载一次 ESP —— 文本模式的 readDeployedEFISignature() 与本函数不能各挂一次。
type efiFacts struct {
	Deployed bool
	Signed   bool
	Signer   string
	SelfSig  bool
	// Line 是给文本模式用的一行说明。
	Line string
}

func collectEFIFacts() efiFacts {
	f := efiFacts{
		Line: tr("unreadable (cannot mount the ESP — administrator rights required)",
			"нечитаемо (не удалось смонтировать ESP — нужны права администратора)",
			"不可读 (无法挂载 ESP — 需管理员权限)"),
	}
	esp := hxcore.MountESP()
	if esp == "" {
		return f
	}
	defer hxcore.UnmountESP(esp)
	data, err := os.ReadFile(esp + `:\EFI\40HX\40HXUNLK.EFI`)
	if err != nil {
		f.Line = tr("not deployed on the ESP (compute unlock inactive; install it via 40HXInstaller.exe)",
			"не развёрнут на ESP (разблокировка вычислений неактивна; установите через 40HXInstaller.exe)",
			"ESP 上未部署 (算力解锁未生效; 用 40HXInstaller.exe 安装)")
		return f
	}
	f.Deployed = true
	sig, perr := hxcore.ReadPESignature(data)
	if perr != nil {
		f.Line = tr("deployed but unreadable as PE: ", "развёрнут, но не читается как PE: ", "已部署但无法按 PE 解析: ") + perr.Error()
		return f
	}
	f.Signed = sig.Present
	f.Signer = sig.SignerCN
	f.SelfSig = sig.SelfSigned
	f.Line = hxcore.FormatSignatureState(sig)
	return f
}

// writeJSONReport 采集状态并输出 JSON, 返回进程退出码:
// 0 = 解锁达成, 1 = 未达成, 2 = 无法判定。脚本可据此直接分支。
//
// 它不自己 os.Exit —— 调用方(runJSON)在拿到码之后还要清理临时拉起的驱动。
func writeJSONReport(st hxcore.UnlockState, drvOK bool, efi efiFacts, aspm string, aspmAC, aspmDC uint32) int {
	rep := jsonReport{
		Schema:    "40hxcheck/v1",
		Version:   hxcore.Version,
		Timestamp: time.Now().Format(time.RFC3339),
		Admin:     isAdmin(),
		OS:        osVersion(),
		GPU: jsonGPU{
			Present40HX: hxcore.FindGPU(),
			GSP:         map[bool]string{true: "enabled", false: "disabled"}[hxcore.GspEnabled()],
		},
		Firmware: jsonFirmware{
			SecureBoot:       hxcore.SecureBootOn(),
			BootMode:         map[bool]string{true: "legacy", false: "uefi"}[hxcore.FirmwareIsLegacy()],
			TestSigning:      hxcore.TestSigningOn(),
			FastStartup:      hxcore.FastStartupOn(),
			UnlockEFISigner:  efi.Signer,
			UnlockEFISelfSig: efi.SelfSig,
		},
		Compute: jsonCompute{
			Measured: st.SS0OK,
			Unlocked: st.Unlocked,
		},
		PCIe: jsonPCIe{
			Measured:    st.Speed >= 1,
			LinkSpeed:   st.Speed,
			LinkWidth:   st.Width,
			TargetSpeed: st.TLS,
			ASPM:        aspm,
			ASPMAC:      aspmAC,
			ASPMDC:      aspmDC,
		},
		ReBAR: jsonReBAR{
			Measured: st.RebarOK,
			Enabled:  st.RebarOn,
			BAR1MiB:  st.RebarSizeMB,
		},
	}
	switch {
	case !efi.Deployed:
		rep.Firmware.UnlockEFI = "not_deployed"
	case efi.Signed:
		rep.Firmware.UnlockEFI = "signed"
	case efi.Signer == "" && efi.Line != "":
		// 部署了、但签名状态读不出来(如 PE 解析失败) — 与"明确未签名"区分开。
		rep.Firmware.UnlockEFI = "unreadable"
	default:
		rep.Firmware.UnlockEFI = "unsigned"
	}
	if st.SS0OK {
		ss0, ss1 := st.SS0, st.SS1
		rep.Compute.SS0, rep.Compute.SS1 = &ss0, &ss1
	}
	if bdf, ok := hxcore.FindGPUBDFFromRegistry(); ok {
		s := fmt.Sprintf("%02X:%02X.%X", (bdf>>8)&0xFF, (bdf>>3)&0x1F, bdf&0x7)
		rep.GPU.BDF = &s
	}

	status := statusEnum(st, drvOK, rep.GPU.Present40HX, rep.Firmware.SecureBoot,
		efi.Signed, efi.Deployed)
	rep.Verdict = jsonVerdict{
		Status:  status,
		Compute: map[bool]string{true: "unlocked", false: "locked"}[st.Unlocked && st.SS0OK],
	}
	switch {
	case st.Speed >= 2:
		rep.Verdict.PCIe = "gen2_reached"
	case st.TLS >= 2:
		rep.Verdict.PCIe = "gen2_target_set"
	case st.Speed == 1:
		rep.Verdict.PCIe = "gen1"
	default:
		rep.Verdict.PCIe = "unknown"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		// 编码失败时 stdout 可能已经写进去半个文档, 所以错误只往 stderr 走,
		// 让调用方的 JSON 解析失败 + 退出码 2 一起表明"这次输出不可用"。
		fmt.Fprintln(os.Stderr, "json encode failed:", err)
		return 2
	}
	switch status {
	case verdictUnlockOK, verdictComputeOnly:
		return 0
	case verdictInconclusive, verdictDriverNotLoaded:
		return 2
	default:
		return 1
	}
}

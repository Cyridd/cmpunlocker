// 40hxcore — PE / Authenticode 签名读取(只读, 不验签)
//
// 用途: Secure Boot 共存路径需要回答两个问题 —
//
//	① 这套固件是否开着 Secure Boot?          → SecureBootOn() (probe.go)
//	② ESP 上那个 40HXUNLK.EFI 到底带没带签名? → 本文件
//
// 为什么不直接调 WinVerifyTrust / Get-AuthenticodeSignature:
// 本项目的签名由用户自己的 db 密钥(sbctl 生成, 自签名)完成。WinVerifyTrust
// 会按"受信任根"验链, 对自签名一律返回 TRUST_E_NOSIGNATURE/不信任 —— 那正是
// 我们要如实报告的情形("已签名, 但签的是你自己的密钥, 固件只认已登记到 db 的"),
// 用系统验签 API 反而说不清。这里只做结构解析:
//
//	· 读 PE Certificate Table (数据目录第 4 项) 判断"有没有签名"
//	· 解析 PKCS#7 SignedData 取出签名者证书的 subject CN, 便于用户核对是不是自己
//	  在 sbctl 里看到的那个 CN
//
// 本文件不涉及任何 Windows API, 因此可以在 Linux 上交叉编译并被单元测试覆盖。
package hxcore

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// PESignature 描述一个 PE 镜像的 Authenticode 签名状态。
type PESignature struct {
	// Present 为 true 表示 Certificate Table 存在且非空 —— 即镜像带签名。
	Present bool
	// Size 是 Certificate Table 的字节长度(含 WIN_CERTIFICATE 头)。
	Size uint32
	// Offset 是 Certificate Table 在文件中的偏移(PE 数据目录里存的是文件偏移, 非 RVA)。
	Offset uint32
	// SignerCN 是签名者证书的 subject Common Name; 解析失败或没有签名时为空。
	SignerCN string
	// SelfSigned 表示签名者证书是自签的(issuer == subject), 这正是 sbctl 密钥的样子。
	SelfSigned bool
	// Note 是给用户看的一行说明(已本地化)。
	Note string
}

// Signed 是 Present 的便捷读法。
func (s PESignature) Signed() bool { return s.Present }

// peCertTableDirIndex 是 Certificate Table 在 Optional Header 数据目录中的下标。
// 4 = IMAGE_DIRECTORY_ENTRY_SECURITY。注意它的 "VirtualAddress" 字段对这项而言
// 是文件偏移(IMAGE_DIRECTORY_ENTRY_SECURITY 是数据目录里唯一不遵循 RVA 约定的项)。
const peCertTableDirIndex = 4

// ReadPESignature 解析 PE 镜像的 Authenticode 签名信息。
//
// 返回的 error 仅用于"这不是一个可解析的 PE"这类结构错误; 镜像没有签名不算错误
// (Present=false 即可), 因为"没签名"是本流程的正常且期望的状态。
func ReadPESignature(data []byte) (PESignature, error) {
	var sig PESignature

	// --- DOS 头: 0x3C 处的 e_lfanew 指向 PE 签名 "PE\0\0" ---
	if len(data) < 0x40 {
		return sig, fmt.Errorf(T("file is too small to be a PE image (%d bytes)", "файл слишком мал для образа PE (%d байт)", "文件太小, 不是 PE 镜像 (%d bytes)"), len(data))
	}
	if !bytes.HasPrefix(data, []byte("MZ")) {
		return sig, errors.New(T("missing MZ header", "нет заголовка MZ", "缺 MZ 头"))
	}
	lfanew := binary.LittleEndian.Uint32(data[0x3C:0x40])
	// 加了上限是防御性的: 损坏的 e_lfanew 会让我们去读越界位置。
	if lfanew == 0 || int(lfanew)+24 > len(data) {
		return sig, fmt.Errorf(T("e_lfanew (0x%08X) points outside the file", "e_lfanew (0x%08X) указывает за пределы файла", "e_lfanew (0x%08X) 超出文件范围"), lfanew)
	}
	if !bytes.Equal(data[lfanew:lfanew+4], []byte("PE\x00\x00")) {
		return sig, fmt.Errorf(T("missing PE\\0\\0 signature at 0x%08X", "нет подписи PE\\0\\0 по адресу 0x%08X", "0x%08X 处缺 PE\\0\\0 签名"), lfanew)
	}

	// --- COFF 头: SizeOfOptionalHeader 决定 Optional Header 是否可信 ---
	coff := lfanew + 4
	sizeOfOptional := binary.LittleEndian.Uint16(data[coff+16 : coff+18])
	opt := coff + 20
	if sizeOfOptional == 0 || int(opt)+int(sizeOfOptional) > len(data) {
		return sig, fmt.Errorf(T("corrupt COFF header (SizeOfOptionalHeader=%d)", "повреждён заголовок COFF (SizeOfOptionalHeader=%d)", "COFF 头损坏 (SizeOfOptionalHeader=%d)"), sizeOfOptional)
	}

	// --- Optional Header: Magic 区分 PE32 / PE32+ ; 数据目录数量跟着变 ---
	// PE32  (0x10b): 数据目录在 optional+96,  NumberOfRvaAndSizes 在 optional+92
	// PE32+ (0x20b): 数据目录在 optional+112, NumberOfRvaAndSizes 在 optional+108
	var dirOff, numOff uint32
	switch binary.LittleEndian.Uint16(data[opt : opt+2]) {
	case 0x10b:
		numOff, dirOff = 92, 96
	case 0x20b:
		numOff, dirOff = 108, 112
	default:
		return sig, fmt.Errorf(T("unknown Optional Header magic 0x%04X (not PE32/PE32+)", "неизвестная магия Optional Header 0x%04X (не PE32/PE32+)", "未知 Optional Header magic 0x%04X (非 PE32/PE32+)"), binary.LittleEndian.Uint16(data[opt:opt+2]))
	}
	if int(numOff)+4 > int(sizeOfOptional) {
		// 没有 NumberOfRvaAndSizes 字段 = 没有数据目录 = 不可能有签名。
		sig.Note = T("PE has no data directories — no embedded signature", "у PE нет каталогов данных — встроенной подписи нет", "PE 无数据目录 — 无内嵌签名")
		return sig, nil
	}
	nDirs := binary.LittleEndian.Uint32(data[opt+numOff : opt+numOff+4])
	if nDirs <= peCertTableDirIndex {
		sig.Note = T("PE declares no certificate table — the image is unsigned", "PE не объявляет таблицу сертификатов — образ без подписи", "PE 未声明证书表 — 镜像未签名")
		return sig, nil
	}
	entry := opt + dirOff + peCertTableDirIndex*8
	// entry 是 uint32, 用 uint64 比较避免在 32 位边界回绕(损坏输入)。
	if uint64(entry)+8 > uint64(len(data)) {
		return sig, errors.New(T("certificate-table directory entry is outside the file", "запись каталога таблицы сертификатов вне файла", "证书表目录项超出文件范围"))
	}
	sig.Offset = binary.LittleEndian.Uint32(data[entry : entry+4])
	sig.Size = binary.LittleEndian.Uint32(data[entry+4 : entry+8])

	// 数据目录里的 (偏移, 大小) 任一为 0 → 无签名。这是最常见的情况, 也是期望情况。
	if sig.Offset == 0 || sig.Size == 0 {
		sig.Note = T("no Authenticode signature (unsigned image)", "нет подписи Authenticode (образ без подписи)", "无 Authenticode 签名 (未签名镜像)")
		return sig, nil
	}
	sig.Present = true

	// 签名数据本身可能被截断(LargeAddressAware 等场景) — 结构上仍算"有签名",
	// 但取不到证书; 下面尽力解析 CN, 失败只留空。
	if end := uint64(sig.Offset) + uint64(sig.Size); end <= uint64(len(data)) {
		if cn, self := signerFromCertTable(data[sig.Offset:end]); cn != "" {
			sig.SignerCN = cn
			sig.SelfSigned = self
		}
	}
	sig.Note = describeSignature(&sig)
	return sig, nil
}

// describeSignature 生成签名状态的一行人类可读说明。
func describeSignature(sig *PESignature) string {
	switch {
	case sig.SignerCN != "" && sig.SelfSigned:
		return fmt.Sprintf(T("signed (self-signed, CN=%s) — firmware only accepts it once this key is enrolled in db",
			"подписано (самоподписанный, CN=%s) — прошивка примет его только после регистрации этого ключа в db",
			"已签名 (自签名, CN=%s) — 仅在把该密钥登记进 db 后固件才会接受"), sig.SignerCN)
	case sig.SignerCN != "":
		return fmt.Sprintf(T("signed (CN=%s)", "подписано (CN=%s)", "已签名 (CN=%s)"), sig.SignerCN)
	default:
		return T("signed (certificate present, signer subject unreadable)",
			"подписано (сертификат есть, субъект не читается)",
			"已签名 (证书存在, 签名者主体无法解析)")
	}
}

// signerFromCertTable 从 PE Certificate Table 中取出签名者证书的 CN。
//
// 表结构: 一串 WIN_CERTIFICATE 记录, 每条 8 字节头
//
//	dwLength(4) wRevision(2) wCertificateType(2) bCertificate[dwLength-8]
//
// 其中 wCertificateType=2 (WIN_CERT_TYPE_PKCS_SIGNED_DATA) 的记录体就是完整的
// PKCS#7 SignedData。现代镜像通常只有一条这样记录。
func signerFromCertTable(tbl []byte) (cn string, selfSigned bool) {
	for off := 0; off+8 <= len(tbl); {
		dwLength := binary.LittleEndian.Uint32(tbl[off : off+4])
		certType := binary.LittleEndian.Uint16(tbl[off+6 : off+8])
		// dwLength 必须至少覆盖自身头部, 且 8 字节对齐; 异常即停止解析,
		// 不尝试"修复" — 损坏的输入只该得到"读不出签名者"。
		if dwLength < 8 || off+int(dwLength) > len(tbl) {
			return "", false
		}
		if certType == 0x0002 { // WIN_CERT_TYPE_PKCS_SIGNED_DATA
			if c, s := signerFromPKCS7(tbl[off+8 : off+int(dwLength)]); c != "" {
				return c, s
			}
		}
		// 记录按 8 字节边界对齐
		off += int(dwLength)
		if r := off % 8; r != 0 {
			off += 8 - r
		}
	}
	return "", false
}

// pkcs7SignedData 只声明我们真正需要的字段, 其余用 asn1.RawValue 直接跳过 —
// 这样不必为 PKCS#7 引入任何依赖, 也不受结构版本差异影响。
//
//	ContentInfo       ::= SEQUENCE { contentType OID, content [0] EXPLICIT ANY }
//	SignedData        ::= SEQUENCE {
//	    version CMSVersion, digestAlgorithms SET, encapContentInfo SEQUENCE,
//	    certificates [0] IMPLICIT CertificateSet OPTIONAL, signerInfos SET }
type pkcs7ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type pkcs7SignedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContentInfo asn1.RawValue
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	SignerInfos      asn1.RawValue
}

// signerFromPKCS7 解析 PKCS#7 SignedData blob, 返回签名者证书的 CN。
//
// 取 CN 的策略: 证书集合里"没有签发过其它证书的那张"就是叶证书(签名者)。
// 这对自签名单证书的情形同样成立(它只签发自己, 仍被判为叶), 而比"取最后一张"
// 更稳 — 集合里可能还带着交叉证书或根证书。
func signerFromPKCS7(blob []byte) (cn string, selfSigned bool) {
	var ci pkcs7ContentInfo
	if rest, err := asn1.Unmarshal(blob, &ci); err != nil || len(rest) != 0 {
		return "", false
	}
	if !ci.ContentType.Equal(oidPKCS7SignedData) || len(ci.Content.Bytes) == 0 {
		return "", false
	}
	var sd pkcs7SignedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return "", false
	}
	if len(sd.Certificates.Bytes) == 0 {
		return "", false
	}
	certs := parseCertificateSet(sd.Certificates.Bytes)
	if len(certs) == 0 {
		return "", false
	}

	// 叶证书 = 其 subject 不是集合中任何其它证书的 issuer 的那一张。
	leaf := certs[len(certs)-1]
	for _, c := range certs {
		issuerOfOthers := false
		for _, o := range certs {
			if o == c {
				continue
			}
			if bytes.Equal(o.RawIssuer, c.RawSubject) {
				issuerOfOthers = true
				break
			}
		}
		if !issuerOfOthers {
			leaf = c
			break
		}
	}
	return leaf.Subject.CommonName, bytes.Equal(leaf.RawIssuer, leaf.RawSubject)
}

// oidPKCS7SignedData = 1.2.840.113549.1.7.2
var oidPKCS7SignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

// parseCertificateSet 遍历 [0] IMPLICIT CertificateSet 里的原始证书。
// 集合里除了 Certificate(正常的 x509 证书)还可能混入扩展项(带 [0] 标签的
// 其它证书格式) — 那种直接跳过, 不当作错误。
func parseCertificateSet(raw []byte) []*x509.Certificate {
	var out []*x509.Certificate
	rest := raw
	for len(rest) > 0 {
		var rv asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &rv)
		if err != nil {
			break
		}
		if rv.Class != asn1.ClassUniversal || rv.Tag != asn1.TagSequence {
			continue // 扩展项(如其它证书格式) — 不解析
		}
		// FullBytes 即完整 DER 编码, x509 解析需要包含外层 SEQUENCE 头。
		if c, cerr := x509.ParseCertificate(rv.FullBytes); cerr == nil {
			out = append(out, c)
		}
	}
	return out
}

// FormatSignatureState 把签名状态压成诊断输出用的一行, 供 check40x / 安装器共用,
// 保证两处措辞一致。
//
// Note 已经是一句完整的话(本身就含"已签名/未签名"的语义), 所以这里不再另外拼一个
// 状态词 —— 否则会输出 "已签名 — 已签名 (自签名, CN=...)" 这种重复。
func FormatSignatureState(sig PESignature) string {
	if n := strings.TrimSpace(sig.Note); n != "" {
		return n
	}
	if sig.Present {
		return T("signed", "подписано", "已签名")
	}
	return T("unsigned", "без подписи", "未签名")
}

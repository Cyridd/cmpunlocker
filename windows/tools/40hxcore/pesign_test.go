package hxcore

// Tests for the PE/Authenticode reader used by the Secure Boot coexistence path.
//
// The signed case is built at runtime from a freshly generated key rather than
// committed as a fixture: a signed PE carries the signer's public certificate,
// and freezing one into the repository would tie a test artifact to a throwaway
// key for no benefit. Generating it here also proves the parser handles real DER
// produced by the standard library, not just bytes we happened to write down.
//
// The unsigned case is checked against the real embed/40HXUNLK.EFI, so a future
// change that accidentally signs the shipped payload (breaking the
// reproducibility gate in .github/workflows/ci.yml) fails here too.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const embeddedEFIPath = "../inst40hx/embed/40HXUNLK.EFI"

// peCertDirIndex mirrors the reader's own constant for the fixture builder.
const peCertDirIndex = 4

// buildTestPE assembles a minimal but structurally valid PE32+ image. When
// certTable is non-nil it is appended and referenced from the Optimical
// Header's data directory, exactly as a real signing tool would do.
func buildTestPE(certTable []byte) []byte {
	const (
		lfanew = 0x80
		optOff = lfanew + 4 + 20 // PE\0\0 + COFF header
		dirOff = 112             // PE32+ data directory offset
		optLen = dirOff + 16*8   // room for 16 directories
	)
	buf := make([]byte, optOff+optLen)
	copy(buf, "MZ")
	binary.LittleEndian.PutUint32(buf[0x3C:], lfanew)
	copy(buf[lfanew:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(buf[lfanew+4+0:], 0x8664)  // Machine = amd64
	binary.LittleEndian.PutUint16(buf[lfanew+4+16:], optLen) // SizeOfOptionalHeader
	binary.LittleEndian.PutUint16(buf[optOff:], 0x20b)       // PE32+
	binary.LittleEndian.PutUint32(buf[optOff+108:], 16)      // NumberOfRvaAndSizes

	if certTable != nil {
		// The Certificate Table directory entry stores a *file offset*, not an
		// RVA — the one directory entry that breaks the usual convention.
		off := len(buf)
		binary.LittleEndian.PutUint32(buf[optOff+dirOff+peCertDirIndex*8:], uint32(off))
		binary.LittleEndian.PutUint32(buf[optOff+dirOff+peCertDirIndex*8+4:], uint32(len(certTable)))
		buf = append(buf, certTable...)
	}
	return buf
}

// wrapWinCert wraps a PKCS#7 blob in a WIN_CERTIFICATE record and pads the
// result to the 8-byte alignment the format requires.
func wrapWinCert(p7 []byte) []byte {
	const hdr = 8
	rec := make([]byte, hdr+len(p7))
	binary.LittleEndian.PutUint32(rec[0:], uint32(hdr+len(p7))) // dwLength (unpadded)
	binary.LittleEndian.PutUint16(rec[4:], 0x0200)              // wRevision
	binary.LittleEndian.PutUint16(rec[6:], 0x0002)              // WIN_CERT_TYPE_PKCS_SIGNED_DATA
	copy(rec[hdr:], p7)
	for len(rec)%8 != 0 {
		rec = append(rec, 0)
	}
	return rec
}

// buildTestPKCS7 produces a DER PKCS#7 SignedData containing one self-signed
// certificate with the given Common Name. This mirrors what sbctl/sbsign
// produce for a self-signed db key, which is the case the reader must describe.
func buildTestPKCS7(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	// The certificate set is a SET OF, so its members must be sorted by their
	// DER encoding — with a single member that is trivially satisfied.
	set := der
	if len(set) > 0 {
		set = append([]byte{}, der...)
	}
	inner, err := asn1.Marshal(struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue `asn1:"optional,tag:0"`
		SignerInfos      asn1.RawValue
	}{
		Version:          1,
		DigestAlgorithms: asn1.RawValue{Class: 0, Tag: 17, IsCompound: true},
		EncapContentInfo: asn1.RawValue{Class: 0, Tag: 16, IsCompound: true, Bytes: nil},
		Certificates:     asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: set},
		SignerInfos:      asn1.RawValue{Class: 0, Tag: 17, IsCompound: true},
	})
	if err != nil {
		t.Fatalf("marshal SignedData: %v", err)
	}
	oid, err := asn1.Marshal(oidPKCS7SignedData)
	if err != nil {
		t.Fatalf("marshal OID: %v", err)
	}
	// ContentInfo ::= SEQUENCE { contentType OID, content [0] EXPLICIT SignedData }
	content, err := asn1.Marshal(asn1.RawValue{
		Class: 2, Tag: 0, IsCompound: true, Bytes: inner,
	})
	if err != nil {
		t.Fatalf("marshal [0] content: %v", err)
	}
	body := append(append([]byte{}, oid...), content...)
	outer, err := asn1.Marshal(asn1.RawValue{
		Class: 0, Tag: 16, IsCompound: true, Bytes: body,
	})
	if err != nil {
		t.Fatalf("marshal ContentInfo: %v", err)
	}
	return outer
}

func TestReadPESignature_UnsignedSynthetic(t *testing.T) {
	sig, err := ReadPESignature(buildTestPE(nil))
	if err != nil {
		t.Fatalf("a valid unsigned PE should parse without error, got %v", err)
	}
	if sig.Present {
		t.Errorf("unsigned PE reported as signed (offset=%d size=%d)", sig.Offset, sig.Size)
	}
	if sig.SignerCN != "" {
		t.Errorf("unsigned PE reported a signer: %q", sig.SignerCN)
	}
	t.Logf("unsigned synthetic image: %s", FormatSignatureState(sig))
}

func TestReadPESignature_SelfSigned(t *testing.T) {
	p7 := buildTestPKCS7(t, "Database Key")
	sig, err := ReadPESignature(buildTestPE(wrapWinCert(p7)))
	if err != nil {
		t.Fatalf("signed PE failed to parse: %v", err)
	}
	if !sig.Present {
		t.Fatal("signed PE was not detected as signed")
	}
	if sig.SignerCN != "Database Key" {
		t.Errorf("SignerCN = %q, want %q", sig.SignerCN, "Database Key")
	}
	if !sig.SelfSigned {
		t.Error("a self-signed certificate was not reported as self-signed")
	}
	t.Logf("signed synthetic image: %s", FormatSignatureState(sig))
}

// TestEmbeddedEFIIsUnsigned guards the CI reproducibility gate: the committed
// payload must stay byte-identical across builds, so it must never carry a
// signature (which would be bound to one operator's private key).
func TestEmbeddedEFIIsUnsigned(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(embeddedEFIPath))
	if err != nil {
		t.Skipf("embedded EFI not available: %v", err)
	}
	if len(data) < 0x2000 {
		t.Fatalf("embedded EFI is suspiciously small (%d bytes)", len(data))
	}
	sig, err := ReadPESignature(data)
	if err != nil {
		t.Fatalf("the committed EFI failed to parse as PE: %v", err)
	}
	if sig.Present {
		t.Errorf("embed/40HXUNLK.EFI must stay unsigned so the build hash keeps reproducing, but it reports: %s", FormatSignatureState(sig))
	}
	t.Logf("embedded EFI (%d bytes): %s", len(data), FormatSignatureState(sig))
}

// TestReadPESignature_Malformed makes sure damaged input is rejected rather
// than panicking: the installer feeds this function files from disk, and the
// checker feeds it whatever it finds on the ESP.
func TestReadPESignature_Malformed(t *testing.T) {
	lfanewOutOfRange := make([]byte, 0x100)
	copy(lfanewOutOfRange, "MZ")
	binary.LittleEndian.PutUint32(lfanewOutOfRange[0x3C:], 0x00FFFFFF)

	zeroOptionalSize := make([]byte, 0x100)
	copy(zeroOptionalSize, "MZ")
	binary.LittleEndian.PutUint32(zeroOptionalSize[0x3C:], 0x40)
	copy(zeroOptionalSize[0x40:], "PE\x00\x00")

	cases := map[string][]byte{
		"empty":               {},
		"truncated MZ":        []byte("MZ"),
		"no MZ header":        make([]byte, 0x100),
		"lfanew out of range": lfanewOutOfRange,
		"zero optional size":  zeroOptionalSize,
	}
	for name, in := range cases {
		in := in
		t.Run(name, func(t *testing.T) {
			sig, err := ReadPESignature(in)
			if err != nil {
				t.Logf("rejected with error (expected): %v", err)
				return
			}
			if sig.Present {
				t.Errorf("malformed input reported as signed")
			}
			t.Logf("accepted as unsigned: %s", FormatSignatureState(sig))
		})
	}
}

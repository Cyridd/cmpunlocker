#!/usr/bin/env bash
# Sign the CMP 40HX EFI application so it runs with Secure Boot enabled.
#
# Why this is a separate script and not part of build_v70.sh:
#
#   build_v70.sh must stay reproducible — its output has to hash to the same
#   value on every machine, which is what .github/workflows/ci.yml checks and
#   what SHA256SUMS.txt in the release package lets users verify.  A signature
#   is bound to *your* private key, so signing necessarily changes the bytes
#   and breaks that property.  Signing is therefore a local, opt-in step that
#   produces a second file, never a replacement for the reproducible one.
#
# The signed file is yours alone: the firmware will only execute it after the
# matching public certificate is enrolled in the platform's `db`.  Do not
# commit it, and do not ship it to anyone else — for them it is just an
# unverifiable binary whose hash does not match the published one.
#
# Usage:
#   sudo ./sign_efi.sh                    # sign the local build_v70.sh output
#   sudo IN=/path/to/40HXUNLK.EFI ./sign_efi.sh
#   sudo OUT=/tmp/mine.signed.efi ./sign_efi.sh
#
# Environment:
#   IN       input EFI (default: unlock40x_v70.efi here, else the embedded copy)
#   OUT      output path       (default: $SRCDIR/40HXUNLK.signed.efi)
#   SBCTL    sbctl binary      (default: sbctl)
#   SBSIGN   sbsign binary     (default: sbsign; used only if sbctl is absent)
#   KEY/CERT sbsign key pair   (default: /var/lib/sbctl/keys/db/db.key/.pem)
#   PYTHON   python3 binary    (default: python3; used for verification only)
set -euo pipefail

SRCDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SRCDIR"

IN="${IN:-}"
if [ -z "$IN" ]; then
    # Prefer the freshly built, reproducible artifact; fall back to the copy the
    # installer embeds, which is byte-identical to a correct build.
    for cand in "$SRCDIR/unlock40x_v70.efi" "$SRCDIR/../inst40hx/embed/40HXUNLK.EFI"; do
        [ -f "$cand" ] || continue
        IN="$cand"
        break
    done
fi
OUT="${OUT:-$SRCDIR/40HXUNLK.signed.efi}"
SBCTL="${SBCTL:-sbctl}"
SBSIGN="${SBSIGN:-sbsign}"
KEY="${KEY:-/var/lib/sbctl/keys/db/db.key}"
CERT="${CERT:-/var/lib/sbctl/keys/db/db.pem}"
PYTHON="${PYTHON:-python3}"

if [ -z "$IN" ] || [ ! -f "$IN" ]; then
    echo "No input EFI found. Run ./build_v70.sh first, or set IN=<path>." >&2
    exit 1
fi

# --- helper: read the PE Authenticode certificate table -----------------------
# Prints "<table offset> <table size> <record length>" when the image carries a
# signature and nothing at all when it does not.  With a second argument it also
# writes the PKCS#7 body of the first record to that path.
#
# This mirrors 40hxcore/pesign.go, which is what 40HXCheck.exe reports from; the
# two must agree, so keep them in sync if the format handling ever changes.
pe_cert_table() {
    "$PYTHON" - "$@" <<'PY'
import struct, sys

data = open(sys.argv[1], 'rb').read()
if len(data) < 0x40 or data[:2] != b'MZ':
    sys.exit(0)
lfanew = struct.unpack_from('<I', data, 0x3C)[0]
if lfanew == 0 or lfanew + 24 > len(data) or data[lfanew:lfanew + 4] != b'PE\0\0':
    sys.exit(0)
coff = lfanew + 4
size_opt = struct.unpack_from('<H', data, coff + 16)[0]
opt = coff + 20
if size_opt == 0 or opt + size_opt > len(data):
    sys.exit(0)
magic = struct.unpack_from('<H', data, opt)[0]
if magic == 0x10b:      # PE32
    num_off, dir_off = 92, 96
elif magic == 0x20b:    # PE32+
    num_off, dir_off = 108, 112
else:
    sys.exit(0)
if num_off + 4 > size_opt:
    sys.exit(0)
# Data directory 4 = IMAGE_DIRECTORY_ENTRY_SECURITY.  Its "VirtualAddress" is a
# file offset, not an RVA — the one entry that breaks the usual convention.
if struct.unpack_from('<I', data, opt + num_off)[0] <= 4:
    sys.exit(0)
off, size = struct.unpack_from('<II', data, opt + dir_off + 4 * 8)
if not off or not size or off + 8 > len(data):
    sys.exit(0)
dwlen, _rev, ctype = struct.unpack_from('<IHH', data, off)
print(off, size, dwlen)
if len(sys.argv) > 2 and ctype == 0x0002 and 8 < dwlen <= size:
    # WIN_CERT_TYPE_PKCS_SIGNED_DATA: the record body is the PKCS#7 blob.
    with open(sys.argv[2], 'wb') as fh:
        fh.write(data[off + 8:off + dwlen])
PY
}

sha() { sha256sum -- "$1" | cut -d' ' -f1; }

# --- preflight ---------------------------------------------------------------
if [ "$(head -c2 -- "$IN")" != "MZ" ]; then
    echo "Not a PE image: $IN" >&2
    exit 1
fi
if [ -n "$(pe_cert_table "$IN" || true)" ]; then
    echo "Input is already signed: $IN" >&2
    echo "Signing twice is not what you want — rebuild a clean unsigned image with ./build_v70.sh." >&2
    exit 1
fi
if [ "$IN" = "$OUT" ]; then
    echo "IN and OUT must differ; the unsigned build must stay intact for hash verification." >&2
    exit 1
fi

signer=""
if command -v "$SBCTL" >/dev/null 2>&1; then
    signer=sbctl
elif command -v "$SBSIGN" >/dev/null 2>&1; then
    signer=sbsign
else
    echo "Neither $SBCTL nor $SBSIGN found. Install sbctl (Arch: pacman -S sbctl) or sbsigntools." >&2
    exit 1
fi

# The private key is root-only by design, so the signing step needs privileges.
# Check before doing any work, so the failure is a clear message and not a
# half-written output file.
if [ ! -r "$KEY" ] && [ "$(id -u)" != 0 ]; then
    echo "Cannot read the signing key ($KEY) as $(id -un). Re-run with sudo." >&2
    exit 1
fi

echo "=== 1. input ==="
printf '  %s\n  %s bytes, sha256 %s\n' "$IN" "$(stat -c%s -- "$IN")" "$(sha "$IN")"

echo "=== 2. sign ($signer) ==="
stage="$(mktemp -d "${TMPDIR:-/tmp}/cmp40-sign.XXXXXXXX")"
trap 'rm -rf -- "$stage"' EXIT
staged="$stage/signed.efi"

case "$signer" in
sbctl)
    # No -s/--save: this file is a throwaway copy, not something sbctl should
    # track and re-sign on every `sbctl sign-all`.  sbctl sandboxes itself with
    # landlock; if that blocks the paths involved, --disable-landlock is the
    # documented escape hatch.
    if ! "$SBCTL" sign -o "$staged" "$IN"; then
        echo "sbctl sign failed; retrying with --disable-landlock" >&2
        "$SBCTL" --disable-landlock sign -o "$staged" "$IN"
    fi
    ;;
sbsign)
    for f in "$KEY" "$CERT"; do
        test -f "$f" || { echo "Missing $f (create keys with: sbctl create-keys)" >&2; exit 1; }
    done
    "$SBSIGN" --key "$KEY" --cert "$CERT" --output "$staged" "$IN"
    ;;
esac
test -s "$staged" || { echo "Signer produced no output" >&2; exit 1; }

echo "=== 3. verify ==="
table="$(pe_cert_table "$staged" "$stage/sig.p7" || true)"
if [ -z "$table" ]; then
    echo "Signed output carries no certificate table — the signature did not take." >&2
    exit 1
fi
set -- $table
printf '  certificate table at offset %s, %s bytes\n' "$1" "$2"
if [ -s "$stage/sig.p7" ] && command -v openssl >/dev/null 2>&1; then
    echo "  signer certificate(s):"
    openssl pkcs7 -inform DER -in "$stage/sig.p7" -print_certs -noout 2>/dev/null |
        sed '/^$/d; s/^/    /'
fi
# sbverify is the only tool here that checks the signature cryptographically
# rather than structurally; run it when it happens to be installed.
if command -v sbverify >/dev/null 2>&1 && [ -f "$CERT" ]; then
    echo "  sbverify:"
    sbverify --cert "$CERT" "$staged" 2>&1 | sed 's/^/    /'
fi

mkdir -p -- "$(dirname "$OUT")"
cp -- "$staged" "$OUT"
# The output is written by root under sudo; hand it back to the invoking user so
# they can copy it to the ESP or feed it to the installer without another sudo.
if [ -n "${SUDO_UID:-}" ]; then
    chown -- "$SUDO_UID:${SUDO_GID:-$SUDO_UID}" "$OUT" || true
fi

cat <<EOF
=== 4. done ===
  $OUT
  $(stat -c%s -- "$OUT") bytes, sha256 $(sha "$OUT")

This hash intentionally differs from the unsigned build and from SHA256SUMS.txt:
the signature is part of the file.  Keep the file local — it is gitignored.

Next steps (see windows/README.md, section "Secure Boot"):
  1. Enroll your keys once, if you have not already:
         sudo sbctl enroll-keys --microsoft
     --microsoft keeps the vendor certificates in db, so the firmware, GPU
     option ROMs and Windows' own boot chain keep validating.
  2. Install with this file instead of the embedded one:
         40HXInstaller.exe -efi <path to $(basename "$OUT")>
     or copy it over \\EFI\\40HX\\40HXUNLK.EFI on the ESP yourself.
  3. Enable Secure Boot in firmware setup, then confirm with:
         40HXCheck.exe -json
     "unlock_efi_state" must read "signed", and "status" must not be
     "secure_boot_blocks_unsigned_efi".
EOF

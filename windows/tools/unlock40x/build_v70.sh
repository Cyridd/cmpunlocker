#!/usr/bin/env bash
# Build the CMP 40HX EFI application with the host's native object format.
set -euo pipefail

SRCDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SRCDIR"

case "$(uname -s)" in
    Linux*)  FORMAT=elf64-x86-64 ;;
    MINGW*|MSYS*|CYGWIN*)
        FORMAT=pe-x86-64
        [ ! -d /mingw64/bin ] || PATH="/mingw64/bin:$PATH"
        ;;
    *) echo "Unsupported host: $(uname -s)" >&2; exit 1 ;;
esac

EFI_INC="${EFI_INC:-/usr/include/efi}"
EFI_LIB="${EFI_LIB:-/usr/lib}"
EFI_LDS="${EFI_LDS:-$EFI_LIB/elf_x86_64_efi.lds}"
OBJ="${OBJ:-$SRCDIR/unlock40x_v70.o}"
OUT="${OUT:-$SRCDIR/unlock40x_v70.so}"
EFIOUT="${EFI_OUT:-$SRCDIR/unlock40x_v70.efi}"
CC="${CC:-gcc}"
OBJCOPY="${OBJCOPY:-objcopy}"
LD_CMD="${LD:-ld}"
if [ "$FORMAT" = elf64-x86-64 ] && command -v ld.bfd >/dev/null 2>&1; then
    LD_CMD="${LD:-ld.bfd}"
fi

for tool in "$CC" "$OBJCOPY" "$LD_CMD" objdump; do
    command -v "$tool" >/dev/null || { echo "Missing tool: $tool" >&2; exit 1; }
done
test -f "$EFI_LIB/libefi.a" || { echo "Missing $EFI_LIB/libefi.a" >&2; exit 1; }
if [ "$FORMAT" = elf64-x86-64 ]; then
    for dep in "$EFI_LDS" "$EFI_LIB/crt0-efi-x86_64.o" "$EFI_LIB/libgnuefi.a"; do
        test -f "$dep" || { echo "Missing $dep (install gnu-efi)" >&2; exit 1; }
    done
    "$LD_CMD" --version | head -1 | grep -q 'GNU ld' || {
        echo "Linux ELF build requires GNU ld.bfd" >&2; exit 1;
    }
fi

# Do not publish a partial EFI or replace a previous build on error.
stage_dir="$(mktemp -d "${TMPDIR:-/tmp}/cmp40-efi.XXXXXXXX")"
trap 'rm -rf -- "$stage_dir"' EXIT
src_obj="$stage_dir/unlock40x_v70.o"
linked="$stage_dir/unlock40x_v70.so"
efi="$stage_dir/unlock40x_v70.efi"

extra_defs=()
if [ "${VBIOS_DUMP:-0}" = 1 ]; then
    # The 1 MiB BAR0 VBIOS capture is optional and disabled for the normal
    # unlock build.  Enable it explicitly with VBIOS_DUMP=1 when needed.
    extra_defs+=(-DVBIOS_DUMP)
fi
if [ "${DIRECT_WRITE_PROBE:-0}" = 1 ]; then
    # Host writes to FEAT_OVR are diagnostic only.  On some locked boards a
    # BAR0 transaction to this block can hard-stall before the booter path;
    # enable it explicitly when collecting probe traces.
    extra_defs+=(-DDIRECT_WRITE_PROBE)
fi
if [ "${PRELOAD_PROBE:-0}" = 1 ]; then
    # The preload probe writes Falcon IMEM/DMEM selector registers and is
    # research-only.  Keep it opt-in because a locked board may stall there.
    extra_defs+=(-DPRELOAD_PROBE)
fi
if [ "$FORMAT" = elf64-x86-64 ]; then
    # gnu-efi headers otherwise leave EFIAPI empty on native GCC.  Firmware
    # service pointers and the custom u40x_entry use the Microsoft x64 ABI.
    extra_defs+=(-DHAVE_USE_MS_ABI -DU40X_GNUEFI_ELF)
    pic_flag=-fpic
else
    pic_flag=-fno-pie
fi

echo "=== 1. compile unlock40x_v70.c ($FORMAT) ==="
"$CC" -c -O2 "$pic_flag" -fno-stack-protector -ffreestanding \
    -fno-asynchronous-unwind-tables -fno-unwind-tables \
    -fshort-wchar -mno-red-zone -maccumulate-outgoing-args \
    -fno-builtin -fno-strict-aliasing -Wno-unused-function \
    -I "$EFI_INC" -I "$EFI_INC/x86_64" \
    -DDIRECT_SEC2 -DRELEASE_BUILD "${extra_defs[@]}" \
    -o "$src_obj" unlock40x_v70.c

echo "=== 2. embed blobs ($FORMAT) ==="
blob_objs=()
embed() {
    local f="$1" target="$2" base="${1//./_}" dest="$stage_dir/$2.o"
    "$OBJCOPY" -I binary -O "$FORMAT" -B i386:x86-64 \
        --redefine-sym "_binary_${base}_start=${target}" \
        --redefine-sym "_binary_${base}_end=${target}_end" \
        --redefine-sym "_binary_${base}_size=${target}_size" \
        "$f" "$dest"
    blob_objs+=("$dest")
    echo "embedded $f -> $target"
}
embed v67_payload.bin         v67_payload_bin
embed booter_ucode_dbg.bin    booter_ucode_dbg
embed booter_ucode_prod.bin   booter_ucode_prod
embed gsp_rm_boot_dbg.bin     gsp_rm_boot_dbg
embed fwsec_ga102.bin         fwsec_ga102_bin
embed fwsec_ga102_sig.bin     fwsec_ga102_sig
embed fwsec_40hx_prod.bin     fwsec_40hx_prod_bin
embed fwsec_40hx_dbg.bin      fwsec_40hx_dbg_bin
embed sec2_ucode_vbios_49.bin sec2_ucode_vbios_49
embed sec2_ucode_vbios_89.bin sec2_ucode_vbios_89
embed bl_gsp_tu102.bin        gsp_bl_tu102

echo "=== 3. link ==="
if [ "$FORMAT" = elf64-x86-64 ]; then
    # crt0 self-relocates the ELF image before calling _entry (SysV ABI).
    # The shim in unlock40x_v70.c forwards to u40x_entry (EFI/MS x64 ABI).
    "$LD_CMD" -nostdlib -z nocombreloc --no-undefined \
        -T "$EFI_LDS" -shared -Bsymbolic -e _start \
        --defsym=_entry=u40x_gnuefi_entry \
        "$EFI_LIB/crt0-efi-x86_64.o" "$src_obj" "${blob_objs[@]}" \
        "$EFI_LIB/libgnuefi.a" "$EFI_LIB/libefi.a" -o "$linked"
    echo "=== 4. convert ELF to EFI PE ==="
    # Keep constants and ELF dynamic relocations consumed by gnu-efi's crt0;
    # the .reloc section is required by the firmware's PE loader.
    "$OBJCOPY" -I elf64-x86-64 -O efi-app-x86_64 \
        -j .text -j .sdata -j .data -j .rodata \
        -j .dynamic -j .dynsym -j .dynstr -j '.rel*' -j .reloc \
        "$linked" "$efi"
else
    # Preserve the native PE/COFF build used by the working MSYS2 release.
    "$LD_CMD" -mi386pep --subsystem 10 --image-base 0 \
        -e u40x_entry -o "$linked" \
        "$src_obj" "${blob_objs[@]}" "$EFI_LIB/libefi.a"
    echo "=== 4. convert PE to EFI ==="
    "$OBJCOPY" -I pei-x86-64 -O efi-app-x86_64 \
        -j .text -j .data -j .rdata -j .reloc "$linked" "$efi"
fi

if ! objdump -p "$efi" | grep -q 'EFI application'; then
    echo "Invalid EFI subsystem: $efi" >&2; exit 1
fi
if ! objdump -h "$efi" | grep -q '[[:space:]]\.reloc[[:space:]]'; then
    echo "Missing EFI base relocation section: $efi" >&2; exit 1
fi
if [ "$FORMAT" = elf64-x86-64 ] && \
   ! objdump -h "$efi" | grep -q '[[:space:]]\.rodata[[:space:]]'; then
    echo "Missing read-only data section: $efi" >&2; exit 1
fi

mkdir -p -- "$(dirname "$OBJ")" "$(dirname "$OUT")" "$(dirname "$EFIOUT")"
cp -- "$src_obj" "$OBJ"
cp -- "$linked" "$OUT"
cp -- "$efi" "$EFIOUT"
echo "EFI build OK: $EFIOUT"

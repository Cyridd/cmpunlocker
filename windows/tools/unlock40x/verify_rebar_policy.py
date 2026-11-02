#!/usr/bin/env python3
"""Exercise production EFI ReBAR policy without firmware or GPU access."""
from pathlib import Path
import json
import os
import re
import subprocess
import tempfile
from verify_cpu_rm_selectors import function


def main():
    source = Path(__file__).with_name('unlock40x_v70.c').read_text()
    installer = (Path(__file__).parents[1] / 'inst40hx' / 'rebar_boot.go').read_text()

    def go_string(name):
        match = re.search(r'^const ' + name + r' = ("[^"\n]*")$', installer, re.MULTILINE)
        if not match:
            raise RuntimeError(f'installer constant not found: {name}')
        return json.loads(match.group(1))

    entry = source[source.index('efi_main(EFI_HANDLE ImageHandle,'):]
    if entry.index('u40x_load_rebar_policy(ImageHandle)') > entry.index('u40x_find_gpu()'):
        raise RuntimeError('policy must be read before GPU discovery/mutation')

    code = r'''
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef wchar_t CHAR16;
typedef void VOID;
typedef void *EFI_HANDLE;
typedef int BOOLEAN;
typedef uint8_t UINT8;
typedef uint64_t UINT64;
typedef uint32_t UINT32;
typedef int32_t INT32;
typedef size_t UINTN;
typedef int EFI_STATUS;
typedef struct {
    const CHAR16 *LoadOptions;
    UINT32 LoadOptionsSize;
    EFI_HANDLE DeviceHandle;
    void *FilePath;
} EFI_LOADED_IMAGE_PROTOCOL;
typedef struct EFI_FILE_PROTOCOL EFI_FILE_PROTOCOL;
struct EFI_FILE_PROTOCOL {
    EFI_STATUS (*Open)(EFI_FILE_PROTOCOL *, EFI_FILE_PROTOCOL **, const CHAR16 *, UINT64, UINT64);
    EFI_STATUS (*Read)(EFI_FILE_PROTOCOL *, UINTN *, void *);
    EFI_STATUS (*Close)(EFI_FILE_PROTOCOL *);
};
typedef struct EFI_SIMPLE_FILE_SYSTEM_PROTOCOL EFI_SIMPLE_FILE_SYSTEM_PROTOCOL;
struct EFI_SIMPLE_FILE_SYSTEM_PROTOCOL {
    EFI_STATUS (*OpenVolume)(EFI_SIMPLE_FILE_SYSTEM_PROTOCOL *, EFI_FILE_PROTOCOL **);
};
#define FALSE 0
#define TRUE 1
#define EFI_ERROR(s) ((s) != 0)
#define CompareMem memcmp
#define EFI_FILE_MODE_READ 1
#define U40X_REBAR_SIZE 0x200000000ULL
#define U40X_REBAR_SELECTOR 7U
static int writes, u40x_li_guid, u40x_fs_guid, fail_at, reads, root_closes, file_closes;
static size_t content_size, content_offset, first_read_limit;
static const void *content;
static BOOLEAN u40x_rebar_file_enabled;
static EFI_LOADED_IMAGE_PROTOCOL li;
static EFI_FILE_PROTOCOL root_file, policy_file;
static EFI_SIMPLE_FILE_SYSTEM_PROTOCOL filesystem;
static int wide_equal(const CHAR16 *a, const CHAR16 *b) {
    while (*a && *a == *b) { ++a; ++b; }
    return *a == *b;
}
static EFI_STATUS handle_protocol(EFI_HANDLE image, void *guid, void **out) {
    (void)image;
    if (guid == &u40x_li_guid) {
        if (fail_at == 1) return 1;
        *out = &li;
    } else if (guid == &u40x_fs_guid) {
        if (fail_at == 2) return 1;
        *out = &filesystem;
    } else abort();
    return 0;
}
static EFI_STATUS open_volume(EFI_SIMPLE_FILE_SYSTEM_PROTOCOL *fs, EFI_FILE_PROTOCOL **out) {
    if (fs != &filesystem) abort();
    if (fail_at == 3) return 1;
    *out = &root_file;
    return 0;
}
static EFI_STATUS open_policy(EFI_FILE_PROTOCOL *root, EFI_FILE_PROTOCOL **out,
                              const CHAR16 *path, UINT64 mode, UINT64 attrs) {
    if (root != &root_file || mode != EFI_FILE_MODE_READ || attrs ||
        !wide_equal(path, L"\\EFI\\40HX\\REBAR.CFG")) abort();
    if (fail_at == 4) return 1;
    *out = &policy_file;
    return 0;
}
static EFI_STATUS read_policy(EFI_FILE_PROTOCOL *file, UINTN *size, void *data) {
    if (file != &policy_file) abort();
    ++reads;
    if ((fail_at == 5 && reads == 1) || (fail_at == 6 && reads == 2)) return 1;
    size_t left = content_size - content_offset;
    if (*size > left) *size = left;
    if (reads == 1 && first_read_limit && *size > first_read_limit) *size = first_read_limit;
    if (*size) memcpy(data, (const char *)content + content_offset, *size);
    content_offset += *size;
    return 0;
}
static EFI_STATUS close_file(EFI_FILE_PROTOCOL *file) {
    if (file == &root_file) ++root_closes;
    else if (file == &policy_file) ++file_closes;
    else abort();
    return 0;
}
static EFI_STATUS free_pool(void *ptr) { (void)ptr; return 0; }
static struct {
    EFI_STATUS (*HandleProtocol)(EFI_HANDLE, void *, void **);
    EFI_STATUS (*FreePool)(void *);
} services = {handle_protocol, free_pool}, *BS = &services;
static void Print(const CHAR16 *s, ...) { (void)s; }
static void u40x_rebar_try(UINT64 size, UINT32 selector) {
    if (size != U40X_REBAR_SIZE || selector != U40X_REBAR_SELECTOR) abort();
    ++writes;
}
'''
    for signature in ('static BOOLEAN u40x_has_load_option(',
                      'static INT32 u40x_rebar_policy_parse(',
                      'static BOOLEAN u40x_rebar_requested(',
                      'static VOID u40x_load_rebar_policy(',
                      'static VOID u40x_cpu_rm_rebar('):
        code += function(source, signature)
    code += r'''
static void run_case(const char *name, const void *options, size_t option_size,
                     const void *file_data, size_t file_size, int failure,
                     size_t read_limit, int expected) {
    li.LoadOptions = options;
    li.LoadOptionsSize = (UINT32)option_size;
    li.DeviceHandle = &filesystem;
    li.FilePath = (void *)1;
    content = file_data;
    content_size = file_size;
    content_offset = 0;
    first_read_limit = read_limit;
    fail_at = failure;
    reads = root_closes = file_closes = writes = 0;
    u40x_load_rebar_policy((EFI_HANDLE)&li);
    if (writes) abort();
    int cached_reads = reads;
    u40x_cpu_rm_rebar((EFI_HANDLE)&li);
    if (writes != expected || reads != cached_reads ||
        root_closes != (failure == 1 || failure == 2 || failure == 3 ? 0 : 1) ||
        file_closes != (failure >= 1 && failure <= 4 ? 0 : 1)) {
        fprintf(stderr, "ReBAR case %s failed: writes=%d reads=%d root-close=%d file-close=%d\n",
                name, writes, reads, root_closes, file_closes);
        exit(1);
    }
}
int main(void) {
    root_file.Open = open_policy;
    root_file.Close = policy_file.Close = close_file;
    policy_file.Read = read_policy;
    filesystem.OpenVolume = open_volume;
    const struct { const CHAR16 *text; int plain, file; } cases[] = {
        {NULL, 0, 1}, {L"", 0, 1}, {L"norebar", 0, 0},
        {L"--experimental-rebar", 1, 1},
        {CMP40_INSTALLER_REBAR_OPTIONS, 1, 1},
        {L"40HXUNLK.EFI\t--experimental-rebar\r\n", 1, 1},
        {L"--experimental-rebar norebar", 0, 0},
        {L"norebar --experimental-rebar", 0, 0},
        {L"40HXUNLK.EFI --experimental-rebar norebar", 0, 0},
        {L"--experimental-rebar-extra", 0, 1},
        {L"prefix--experimental-rebar", 0, 1},
    };
    for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
        size_t chars = 0;
        if (cases[i].text) while (cases[i].text[chars]) ++chars;
        size_t size = cases[i].text ? (chars + 1) * sizeof(CHAR16) : 0;
        run_case("plain options", cases[i].text, size, NULL, 0, 4, 0, cases[i].plain);
        run_case("options and ESP policy", cases[i].text, size,
                 CMP40_POLICY_ON, sizeof(CMP40_POLICY_ON) - 1, 0, 0, cases[i].file);
    }
    const UINT8 windows_options[136] = {0x57,0x49,0x4e,0x44,0x4f,0x57,0x53,0,
                                      1,0,0,0,0x88,0,0,0};
    run_case("Windows OptionalData without policy", windows_options, sizeof(windows_options), NULL, 0, 4, 0, 0);
    run_case("Windows OptionalData enabled", windows_options, sizeof(windows_options), CMP40_POLICY_ON, sizeof(CMP40_POLICY_ON) - 1, 0, 0, 1);
    run_case("Windows OptionalData disabled", windows_options, sizeof(windows_options), CMP40_POLICY_OFF, sizeof(CMP40_POLICY_OFF) - 1, 0, 0, 0);
    for (int failure = 1; failure <= 6; ++failure)
        run_case("firmware/filesystem failure", NULL, 0, CMP40_POLICY_ON, sizeof(CMP40_POLICY_ON) - 1, failure, 0, 0);
    const char unknown[] = "CMP40HX_REBAR_V2=1\n";
    const char trailing[] = CMP40_POLICY_ON "junk";
    run_case("empty policy", NULL, 0, NULL, 0, 0, 0, 0);
    run_case("unknown version", NULL, 0, unknown, sizeof(unknown) - 1, 0, 0, 0);
    run_case("truncated policy", NULL, 0, CMP40_POLICY_ON, sizeof(CMP40_POLICY_ON) - 2, 0, 0, 0);
    run_case("embedded terminator", NULL, 0, CMP40_POLICY_ON, sizeof(CMP40_POLICY_ON), 0, 0, 0);
    run_case("trailing bytes", NULL, 0, trailing, sizeof(trailing) - 1, 0, 0, 0);
    run_case("partial read with trailing bytes", NULL, 0, trailing, sizeof(trailing) - 1, 0, sizeof(CMP40_POLICY_ON) - 1, 0);
    puts("ReBAR policy passed: installer bytes, UTF-16/binary options, ESP opt-in, norebar priority, malformed files, read errors and pre-GPU cache (no hardware access)");
    return 0;
}
'''
    with tempfile.TemporaryDirectory(prefix='cmp40-rebar-policy-') as tmp:
        c, exe = Path(tmp) / 'policy.c', Path(tmp) / 'policy'
        c.write_text(code)
        subprocess.run([os.environ.get('CC', 'cc'), '-std=c11', '-Wall', '-Wextra',
                        '-Werror', '-fshort-wchar',
                        '-DCMP40_INSTALLER_REBAR_OPTIONS=L' + json.dumps(go_string('rebarEnableLoadOptions')),
                        '-DCMP40_POLICY_ON=' + json.dumps(go_string('rebarPolicyEnabled')),
                        '-DCMP40_POLICY_OFF=' + json.dumps(go_string('rebarPolicyDisabled')),
                        str(c), '-o', str(exe)], check=True)
        subprocess.run([str(exe)], check=True)


if __name__ == '__main__':
    main()

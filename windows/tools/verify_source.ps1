param([string]$Root)

$ErrorActionPreference = 'Stop'

# The same script lives under tools/ in a checkout and at the root of a package.
if (-not $Root) {
    $Root = $PSScriptRoot
    if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'inst40hx/go.mod') -PathType Leaf) {
        $Root = Split-Path -Parent $PSScriptRoot
    }
}
$directory = (Resolve-Path -LiteralPath $Root).ProviderPath
$manifest = Join-Path $directory 'SHA256SUMS.txt'

if (Test-Path -LiteralPath $manifest -PathType Leaf) {
    $seen = @{}
    foreach ($line in [IO.File]::ReadAllLines($manifest)) {
        if ($line -notmatch '^([a-fA-F0-9]{64}) [ *](.+)$') {
            throw 'Invalid entry in SHA256SUMS.txt.'
        }
        $expected = $Matches[1]
        $name = $Matches[2].Replace('\', '/')
        if ($seen.ContainsKey($name) -or $name -eq 'SHA256SUMS.txt') {
            throw "Duplicate or self-referencing manifest entry: $name"
        }
        $path = $directory
        foreach ($part in $name.Split('/')) {
            if (!$part -or $part -match '[<>:"|?*\x00-\x1f]|[. ]$') {
                throw "Invalid package path: $name"
            }
            $path = Join-Path $path $part
            $item = Get-Item -LiteralPath $path -Force
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Package path contains a link: $name"
            }
        }
        if ($item.PSIsContainer -or (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash -ne $expected) {
            throw "File does not match SHA256SUMS.txt: $name"
        }
        $seen[$name] = $true
    }
    if (!$seen.Count) { throw 'Empty SHA256SUMS.txt.' }
    Write-Output "Verified $($seen.Count) package files."
    return
}

if (!(Test-Path -LiteralPath (Join-Path $directory 'tools/inst40hx/go.mod') -PathType Leaf)) {
    throw 'Missing SHA256SUMS.txt. For a source checkout, pass -Root with the windows directory.'
}
$embedded = 'tools/inst40hx/embed/40HXUNLK.EFI'
$required = @(
    $embedded,
    'efi-rust/Cargo.toml',
    'efi-rust/Cargo.lock',
    'efi-rust/core/src/lib.rs',
    'efi-rust/app/src/full.rs',
    'efi-rust/app/build.rs',
    'efi-rust/app/prepare_assets.py',
    'efi-rust/build_installer_efi.sh',
    'efi-rust/tests/compare_installer_efi.py',
    'tools/40hxcore/windows_args.go',
    'tools/40hxcore/gsp.go',
    'tools/unlock40x/v67_payload.bin',
    'tools/unlock40x/booter_ucode_prod.bin',
    'tools/unlock40x/gsp_rm_boot_dbg.bin',
    'tools/unlock40x/fwsec_40hx_prod.bin',
    'tools/unlock40x/bl_gsp_tu102.bin',
    'tools/unlock40x/unlock40x_v70.c',
    'tools/unlock40x/selector_control.h',
    'tools/unlock40x/handoff_flr.h',
    'tools/unlock40x/payload_control.h',
    'tools/unlock40x/fwsec_sb.h',
    'tools/unlock40x/verify_selector_payload.py',
    'tools/unlock40x/verify_cpu_rm_metadata.py',
    'tools/unlock40x/verify_cpu_rm_selectors.py',
    'tools/unlock40x/build_v70.sh'
)
foreach ($name in $required) {
    if (!(Test-Path -LiteralPath (Join-Path $directory $name) -PathType Leaf)) {
        throw "Missing source input: $name"
    }
}
$hash = Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $directory $embedded)
Write-Output ("{0}  {1}" -f $hash.Hash.ToLowerInvariant(), $embedded)
Write-Output 'Required source files: OK'

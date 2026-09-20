param(
    [string]$Root = (Join-Path $PSScriptRoot "..")
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path $Root).Path
$efi = Join-Path $root "tools\inst40hx\embed\40HXUNLK.EFI"
$sourceEfi = Join-Path $root "tools\unlock40x\unlock40x_v70.efi"

Write-Host "CMP 40HX fork source verification"
Write-Host "Root: $root"

foreach ($path in @($efi, $sourceEfi)) {
    if (Test-Path $path) {
        $hash = Get-FileHash -Algorithm SHA256 $path
        Write-Host ("{0}  {1}" -f $hash.Hash.ToLowerInvariant(), $path)
    } else {
        Write-Host ("MISSING  {0}" -f $path)
    }
}

$required = @(
    "tools\40hxcore\windows_args.go",
    "tools\40hxcore\gsp.go",
    "tools\unlock40x\unlock40x_v70.c",
    "tools\unlock40x\build_v70.sh"
)
$missing = @($required | Where-Object { -not (Test-Path (Join-Path $root $_)) })
if ($missing.Count -gt 0) {
    throw ("Missing required source files:`n" + ($missing -join "`n"))
}

Write-Host "Required source files: OK"
Write-Host "Note: this script verifies source/build inputs only; it does not execute the EFI payload."

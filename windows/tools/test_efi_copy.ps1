$ErrorActionPreference = 'Stop'
$dir = Join-Path $env:TEMP ('cmp40-copy-test-' + [Guid]::NewGuid().ToString('N'))
$helper = Join-Path $PSScriptRoot '..\release-files\copy_efi_verified.ps1'
[IO.Directory]::CreateDirectory($dir) | Out-Null
try {
    $src = Join-Path $dir 'source.efi'
    $dst = Join-Path $dir 'bootx64.efi'
    $bak = $dst + '.40hx.bak'
    [IO.File]::WriteAllText($src, 'new EFI contents')
    [IO.File]::WriteAllText($dst, 'original EFI contents')
    function Copy-Expect([string]$from, [string]$to, [bool]$noReplace, [bool]$success) {
        $copyArgs = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $helper, '-Source', $from, '-Destination', $to)
        if ($noReplace) { $copyArgs += '-NoReplace' }
        & powershell.exe @copyArgs
        if (($LASTEXITCODE -eq 0) -ne $success) { throw "Unexpected copy result: $LASTEXITCODE" }
    }
    Copy-Expect $dst $bak $true $true
    if ([IO.File]::ReadAllText($dst) -ne 'original EFI contents') { throw 'Backup moved the live file.' }
    Copy-Expect $src $bak $true $false
    if ([IO.File]::ReadAllText($bak) -ne 'original EFI contents') { throw 'Existing backup overwritten.' }
    $lock = [IO.File]::Open($dst, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try { Copy-Expect $src $dst $false $false } finally { $lock.Dispose() }
    if ([IO.File]::ReadAllText($dst) -ne 'original EFI contents') { throw 'Locked destination changed.' }
    Copy-Expect $src $dst $false $true
    if ([IO.File]::ReadAllText($dst) -ne 'new EFI contents') { throw 'Replacement failed.' }
    Copy-Expect $bak $dst $false $true
    if ([IO.File]::ReadAllText($dst) -ne 'original EFI contents') { throw 'Restore failed.' }
    [IO.File]::WriteAllText($src, '')
    Copy-Expect $src $dst $false $false
    if ([IO.File]::ReadAllText($dst) -ne 'original EFI contents') { throw 'Empty source replaced the live file.' }
    if ((Get-ChildItem -LiteralPath $dir -Filter '.40hx-stage-*').Count -ne 0) { throw 'Staging file leaked.' }
} finally {
    Remove-Item -LiteralPath $dir -Recurse -Force
}
# Expected helper failures leave LASTEXITCODE nonzero; return the test result.
Write-Output 'EFI copy tests passed: backup, no-overwrite, sharing violation, replace, restore, empty source.'
exit 0

param(
    [Parameter(Mandatory=$true)][string]$Source,
    [Parameter(Mandatory=$true)][string]$Destination,
    [switch]$NoReplace
)
$ErrorActionPreference = 'Stop'
$stage = $null
try {
    # Stage on the destination volume; never truncate the live boot file.
    $destinationPath = [IO.Path]::GetFullPath($Destination)
    $data = [IO.File]::ReadAllBytes([IO.Path]::GetFullPath($Source))
    if ($data.Length -eq 0) { throw 'Refusing an empty EFI source; the destination was preserved.' }
    $stage = [IO.Path]::Combine([IO.Path]::GetDirectoryName($destinationPath), '.40hx-stage-' + [Guid]::NewGuid().ToString('N'))
    $stream = [IO.File]::Open($stage, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $stream.Write($data, 0, $data.Length)
        $stream.Flush($true)
    } finally { $stream.Dispose() }
    $hash = [Security.Cryptography.SHA256]::Create()
    try {
        $expected = [BitConverter]::ToString($hash.ComputeHash($data))
        $actual = [BitConverter]::ToString($hash.ComputeHash([IO.File]::ReadAllBytes($stage)))
    } finally { $hash.Dispose() }
    if ($actual -ne $expected) { throw 'Staged EFI verification failed; the destination was preserved.' }
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class Cmp40EfiMove {
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool MoveFileEx(string source, string destination, uint flags);
}
'@
    [uint32]$flags = 8 # MOVEFILE_WRITE_THROUGH; no cross-volume copy fallback
    if (-not $NoReplace) { $flags = $flags -bor 1 }
    if (-not [Cmp40EfiMove]::MoveFileEx($stage, $destinationPath, $flags)) {
        throw [ComponentModel.Win32Exception]::new([Runtime.InteropServices.Marshal]::GetLastWin32Error())
    }
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
} finally {
    if ($stage -and [IO.File]::Exists($stage)) { [IO.File]::Delete($stage) }
}

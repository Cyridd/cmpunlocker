param([Parameter(Mandatory = $true)][string]$Directory)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath $Directory).ProviderPath.TrimEnd([IO.Path]::DirectorySeparatorChar)
$manifest = Join-Path $root 'SHA256SUMS.txt'
$lines = foreach ($file in (Get-ChildItem -LiteralPath $root -File -Recurse -Force | Sort-Object FullName)) {
    if ($file.FullName -eq $manifest) { continue }
    $name = $file.FullName.Substring($root.Length + 1).Replace('\', '/')
    if ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw "Package path contains a link: $name"
    }
    $hash = Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName
    '{0}  {1}' -f $hash.Hash.ToLowerInvariant(), $name
}
if (!$lines) { throw 'No package files to hash.' }

# GNU sha256sum and Windows PowerShell can read UTF-8 without a BOM.
[IO.File]::WriteAllText($manifest, ($lines -join [char]10) + [char]10, [Text.UTF8Encoding]::new($false))

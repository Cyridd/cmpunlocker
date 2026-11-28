$ErrorActionPreference = 'Stop'
$package = Join-Path ([IO.Path]::GetTempPath()) ('package-' + [Guid]::NewGuid().ToString('N'))
$docs = Join-Path $package 'docs'
$verifier = Join-Path $package 'verify_source.ps1'
$writer = Join-Path $PSScriptRoot 'write_package_manifest.ps1'

function Assert-VerificationFails {
    try { & $verifier | Out-Null } catch { return }
    throw 'Verification accepted a damaged or incomplete package.'
}

[void][IO.Directory]::CreateDirectory($docs)
try {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'verify_source.ps1') -Destination $verifier
    # Keep the script readable by Windows PowerShell 5.1 in any code page.
    $name = 'EFI' + [char]0x5e94 + [char]0x6025 + '.md'
    $guide = Join-Path $docs $name
    [IO.File]::WriteAllText($guide, 'original')
    & $writer -Directory $package
    $manifest = Join-Path $package 'SHA256SUMS.txt'
    $hash = (Get-FileHash -LiteralPath $guide -Algorithm SHA256).Hash.ToLowerInvariant()
    if ([IO.File]::ReadAllLines($manifest) -cnotcontains "$hash  docs/$name") {
        throw 'The manifest lost the Unicode filename or relative directory.'
    }
    & $verifier

    [IO.File]::WriteAllText($guide, 'modified')
    Assert-VerificationFails
    [IO.File]::Delete($guide)
    Assert-VerificationFails
    [IO.File]::Delete($manifest)
    Assert-VerificationFails
} finally {
    Remove-Item -LiteralPath $package -Recurse -Force
}

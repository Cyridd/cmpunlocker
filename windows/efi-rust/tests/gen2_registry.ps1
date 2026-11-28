# SPDX-License-Identifier: MIT
[CmdletBinding()]
param(
    [ValidateSet('Inspect', 'Apply', 'Restore')][string]$Action = 'Inspect',
    [ValidateSet('Policy', 'PolicyPlatform')][string]$Trial = 'Policy',
    [string]$BackupPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-Cmp40Trial {
    param([ValidateSet('Policy', 'PolicyPlatform')][string]$Name)
    # RMPcieLinkSpeed is a bitfield: 1 forces Gen2; 8 disables Gen3.
    @(
        [pscustomobject]@{ Name = 'RMPcieLinkSpeed'; Planned = 9 }
        [pscustomobject]@{ Name = 'RMNoGen2'; Planned = 0 }
        [pscustomobject]@{ Name = 'RmDisableGen2LinkRetraining'; Planned = 0 }
    )
    if ($Name -eq 'PolicyPlatform') {
        [pscustomobject]@{ Name = 'RmForceEnableGen2'; Planned = 1 }
    }
}

function Read-Cmp40Dword {
    param($Key, [string]$Name)
    if ($Key.GetValueNames() -notcontains $Name) {
        return [pscustomobject]@{ Present = $false; Data = $null }
    }
    if ($Key.GetValueKind($Name) -ne [Microsoft.Win32.RegistryValueKind]::DWord) {
        throw "Refusing a non-DWORD value: $Name"
    }
    [pscustomobject]@{ Present = $true; Data = [int32]$Key.GetValue($Name) }
}

function Test-Cmp40Original {
    param($Current, $Entry)
    ($Current.Present -eq $Entry.Present) -and
        ((-not $Current.Present) -or ($Current.Data -eq $Entry.Data))
}

function Get-Cmp40Entries {
    param($Key, [string]$Name)
    foreach ($item in @(Get-Cmp40Trial $Name)) {
        $current = Read-Cmp40Dword $Key $item.Name
        [pscustomobject]@{
            Name = $item.Name; Planned = $item.Planned
            Present = $current.Present; Data = $current.Data
        }
    }
}

function Assert-Cmp40Backup {
    param($Backup)
    if ($Backup.Format -cne 'CMP40_GEN2_REGISTRY_V1' -or
        $Backup.DriverVersion -cne '32.0.16.2114' -or
        $Backup.DriverKey -notmatch '^\{4d36e968-e325-11ce-bfc1-08002be10318\}\\[0-9]{4}$' -or
        $Backup.Trial -cnotin @('Policy', 'PolicyPlatform')) {
        throw 'Invalid backup identity or format.'
    }
    $plan = @(Get-Cmp40Trial $Backup.Trial)
    $entries = @($Backup.Values)
    if ($entries.Count -ne $plan.Count) { throw 'Invalid backup value count.' }
    for ($i = 0; $i -lt $plan.Count; $i++) {
        $entry = $entries[$i]
        if ($entry.Name -cne $plan[$i].Name -or $entry.Planned -ne $plan[$i].Planned -or
            $entry.Present -isnot [bool]) { throw 'Invalid backup value contract.' }
        if ($entry.Present) {
            if (($entry.Data -isnot [int32] -and $entry.Data -isnot [int64]) -or
                $entry.Data -lt [int32]::MinValue -or $entry.Data -gt [int32]::MaxValue) {
                throw 'Invalid original DWORD in backup.'
            }
        } elseif ($null -ne $entry.Data) { throw 'Missing original DWORD must have null data.' }
    }
}

function Restore-Cmp40Values {
    param($Key, [object[]]$Entries)
    # Preflight the whole set before restoring any value. Preserve later edits.
    foreach ($entry in $Entries) {
        $current = Read-Cmp40Dword $Key $entry.Name
        if (-not (Test-Cmp40Original $current $entry) -and
            (-not $current.Present -or $current.Data -ne $entry.Planned)) {
            throw "Value changed outside this trial; restore refused: $($entry.Name)"
        }
    }
    foreach ($entry in $Entries) {
        $current = Read-Cmp40Dword $Key $entry.Name
        if (Test-Cmp40Original $current $entry) { continue }
        if (-not $current.Present -or $current.Data -ne $entry.Planned) {
            throw "Value changed during restore: $($entry.Name)"
        }
        if ($entry.Present) {
            $Key.SetValue($entry.Name, [int32]$entry.Data, [Microsoft.Win32.RegistryValueKind]::DWord)
        } else {
            $Key.DeleteValue($entry.Name, $false)
        }
        if (-not (Test-Cmp40Original (Read-Cmp40Dword $Key $entry.Name) $entry)) {
            throw "Restore readback failed: $($entry.Name)"
        }
    }
    $Key.Flush()
}

function Set-Cmp40Values {
    param($Key, [object[]]$Entries)
    try {
        foreach ($entry in $Entries) {
            if (-not (Test-Cmp40Original (Read-Cmp40Dword $Key $entry.Name) $entry)) {
                throw "Value changed before apply: $($entry.Name)"
            }
        }
        foreach ($entry in $Entries) {
            if (-not (Test-Cmp40Original (Read-Cmp40Dword $Key $entry.Name) $entry)) {
                throw "Value changed during apply: $($entry.Name)"
            }
            $Key.SetValue($entry.Name, [int32]$entry.Planned, [Microsoft.Win32.RegistryValueKind]::DWord)
            $current = Read-Cmp40Dword $Key $entry.Name
            if (-not $current.Present -or $current.Data -ne $entry.Planned) {
                throw "Apply readback failed: $($entry.Name)"
            }
        }
        $Key.Flush()
    } catch {
        $failure = $_.Exception.Message
        try { Restore-Cmp40Values $Key $Entries }
        catch { throw "$failure Rollback incomplete: $($_.Exception.Message). Keep the backup." }
        throw "$failure Original values restored; keep the backup."
    }
}

function Write-Cmp40Backup {
    param($Backup, [string]$Path)
    Assert-Cmp40Backup $Backup
    $json = $Backup | ConvertTo-Json -Depth 5
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes($json)
    $stream = [IO.File]::Open([IO.Path]::GetFullPath($Path),
        [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $stream.Write($bytes, 0, $bytes.Length); $stream.Flush($true) }
    finally { $stream.Dispose() }
    if ([IO.File]::ReadAllText($Path) -cne $json) { throw 'Backup readback failed; no registry writes.' }
    Assert-Cmp40Backup ([IO.File]::ReadAllText($Path) | ConvertFrom-Json)
}

function Get-Cmp40DriverKey {
    param($Registry)
    $devices = @(Get-PnpDevice -Class Display -PresentOnly -ErrorAction Stop |
        Where-Object { $_.InstanceId -match '^PCI\\VEN_10DE&DEV_1F0B(?:&|\\)' })
    if ($devices.Count -ne 1) { throw 'Exactly one present CMP40HX display device is required.' }
    $enum = $Registry.OpenSubKey('SYSTEM\CurrentControlSet\Enum\' + $devices[0].InstanceId, $false)
    if ($null -eq $enum) { throw 'CMP40HX device registry key is unavailable.' }
    try {
        if ($enum.GetValue('Service') -ine 'nvlddmkm') { throw 'CMP40HX is not bound to NVIDIA.' }
        $driver = [string]$enum.GetValue('Driver')
        if ($driver -notmatch '^\{4d36e968-e325-11ce-bfc1-08002be10318\}\\[0-9]{4}$') {
            throw 'Invalid CMP40HX display-driver key.'
        }
        return $driver
    } finally { $enum.Dispose() }
}

function Invoke-Cmp40Registry {
    if ($env:OS -ne 'Windows_NT' -or [Environment]::Is64BitProcess -eq $false) {
        throw 'Run this script in 64-bit Windows PowerShell.'
    }
    if ($Action -ne 'Inspect') {
        if ([string]::IsNullOrWhiteSpace($BackupPath)) { throw 'Apply and Restore require -BackupPath.' }
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
        try { $admin = [Security.Principal.WindowsPrincipal]::new($identity).IsInRole(
            [Security.Principal.WindowsBuiltInRole]::Administrator) }
        finally { $identity.Dispose() }
        if (-not $admin) { throw 'Apply and Restore require an administrator terminal.' }
    }
    $registry = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::LocalMachine, [Microsoft.Win32.RegistryView]::Registry64)
    try {
        $driver = Get-Cmp40DriverKey $registry
        $key = $registry.OpenSubKey('SYSTEM\CurrentControlSet\Control\Class\' + $driver, $Action -ne 'Inspect')
        if ($null -eq $key) { throw 'CMP40HX software key is unavailable.' }
        try {
            $version = [string]$key.GetValue('DriverVersion')
            Write-Host "CMP40HX NVIDIA driver: $version"
            if ($Action -eq 'Inspect') {
                foreach ($name in @('RMPcieLinkSpeed', 'RMNoGen2', 'RmDisableGen2LinkRetraining',
                    'RmForceEnableGen2', 'RMPcieStickyGenSpeed', 'RMPcieGenSwitchOnPmu',
                    'RMBifDetectSpeedSwitchFailure')) {
                    if ($key.GetValueNames() -notcontains $name) { Write-Host "$name = absent" }
                    elseif ($key.GetValueKind($name) -eq [Microsoft.Win32.RegistryValueKind]::DWord) {
                        Write-Host "$name = DWORD $($key.GetValue($name))"
                    } else { Write-Host "$name = non-DWORD (apply will refuse owned values)" }
                }
                return
            }
            if ($version -cne '32.0.16.2114') { throw 'This experiment is limited to NVIDIA 621.14.' }
            if ($Action -eq 'Apply') {
                $backup = [pscustomobject]@{
                    Format = 'CMP40_GEN2_REGISTRY_V1'; DriverKey = $driver
                    DriverVersion = $version; Trial = $Trial
                    Values = @(Get-Cmp40Entries $key $Trial)
                }
                Write-Cmp40Backup $backup $BackupPath
                Set-Cmp40Values $key $backup.Values
                Write-Host "Applied $Trial. Backup retained. Cold-boot the same EFI image before testing."
            } else {
                $backup = [IO.File]::ReadAllText($BackupPath) | ConvertFrom-Json
                Assert-Cmp40Backup $backup
                if ($backup.DriverKey -ine $driver) { throw 'Backup does not match the current CMP40HX driver key.' }
                Restore-Cmp40Values $key @($backup.Values)
                Write-Host 'Original values restored. Backup retained. Cold-boot before the next trial.'
            }
        } finally { $key.Dispose() }
    } finally { $registry.Dispose() }
}

# Dot-sourcing exposes the model-tested routines without registry access.
if ($MyInvocation.InvocationName -ne '.') {
    try { Invoke-Cmp40Registry }
    catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }
}

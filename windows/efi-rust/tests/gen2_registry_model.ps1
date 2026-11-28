# SPDX-License-Identifier: MIT
param([Parameter(Mandatory = $true)][string]$ScratchDirectory)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'gen2_registry.ps1')

class Cmp40RegistryModel {
    [hashtable]$Values = @{}
    [hashtable]$Kinds = @{}
    [string]$SkipOnce = ''
    [int]$Writes = 0
    [string[]] GetValueNames() { return [string[]]$this.Values.Keys }
    [Microsoft.Win32.RegistryValueKind] GetValueKind([string]$name) { return $this.Kinds[$name] }
    [object] GetValue([string]$name) { return $this.Values[$name] }
    [void] SetValue([string]$name, [object]$value, [Microsoft.Win32.RegistryValueKind]$kind) {
        $this.Writes++
        if ($this.SkipOnce -eq $name) { $this.SkipOnce = ''; return }
        $this.Values[$name] = $value
        $this.Kinds[$name] = $kind
    }
    [void] DeleteValue([string]$name, [bool]$throwOnMissing) {
        $this.Writes++
        $this.Values.Remove($name)
        $this.Kinds.Remove($name)
    }
    [void] Flush() {}
    [void] Dispose() {}
}
class Cmp40RegistryRootModel {
    [Cmp40RegistryModel]$Key
    Cmp40RegistryRootModel([Cmp40RegistryModel]$key) { $this.Key = $key }
    [object] OpenSubKey([string]$path, [bool]$writable) { return $this.Key }
}

function Assert-Model {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}
function Expect-ModelFailure {
    param([scriptblock]$Run, [string]$Pattern)
    $failure = $null
    try { $null = & $Run } catch { $failure = $_.Exception.Message }
    Assert-Model ($null -ne $failure -and $failure -match $Pattern) "Expected failure: $Pattern"
}

$policy = @(Get-Cmp40Trial 'Policy')
$platform = @(Get-Cmp40Trial 'PolicyPlatform')
Assert-Model ($policy.Count -eq 3 -and $policy[0].Planned -eq 9 -and $platform.Count -eq 4) 'Trial contract'

$key = [Cmp40RegistryModel]::new()
$key.SetValue('RMPcieLinkSpeed', [int32]::MinValue, [Microsoft.Win32.RegistryValueKind]::DWord)
$key.SetValue('Unrelated', 123, [Microsoft.Win32.RegistryValueKind]::DWord)
$entries = @(Get-Cmp40Entries $key 'PolicyPlatform')
Set-Cmp40Values $key $entries
Assert-Model ($key.GetValue('RMPcieLinkSpeed') -eq 9 -and $key.GetValue('RmForceEnableGen2') -eq 1) 'Applied trial'
Restore-Cmp40Values $key $entries
Assert-Model ($key.GetValue('RMPcieLinkSpeed') -eq [int32]::MinValue) 'Signed DWORD restoration'
Assert-Model ($key.GetValueNames() -notcontains 'RmForceEnableGen2') 'Originally absent DWORD removed'
Assert-Model ($key.GetValue('Unrelated') -eq 123) 'Unrelated value preserved'
$writes = $key.Writes
Restore-Cmp40Values $key $entries
Assert-Model ($key.Writes -eq $writes) 'Repeated restore writes nothing'

$key = [Cmp40RegistryModel]::new()
$entries = @(Get-Cmp40Entries $key 'Policy')
$key.SkipOnce = 'RMNoGen2'
Expect-ModelFailure { Set-Cmp40Values $key $entries } 'Apply readback failed.*Original values restored'
Assert-Model ($key.GetValueNames().Count -eq 0) 'Failed apply rolled back earlier writes'

$key = [Cmp40RegistryModel]::new()
$entries = @(Get-Cmp40Entries $key 'Policy')
Set-Cmp40Values $key $entries
$key.SetValue('RMNoGen2', 7, [Microsoft.Win32.RegistryValueKind]::DWord)
$writes = $key.Writes
Expect-ModelFailure { Restore-Cmp40Values $key $entries } 'changed outside this trial'
Assert-Model ($key.Writes -eq $writes -and $key.GetValue('RMNoGen2') -eq 7) 'Restore preserves later changes'

$key = [Cmp40RegistryModel]::new()
$key.SetValue('RMNoGen2', 'unexpected', [Microsoft.Win32.RegistryValueKind]::String)
$writes = $key.Writes
Expect-ModelFailure { Get-Cmp40Entries $key 'Policy' } 'non-DWORD'
Assert-Model ($key.Writes -eq $writes) 'Type mismatch causes no writes'

$script:ModelDevices = @([pscustomobject]@{ InstanceId = 'PCI\VEN_10DE&DEV_1F0B\SYNTHETIC' })
function Get-PnpDevice { param($Class, [switch]$PresentOnly, $ErrorAction); $script:ModelDevices }
$enum = [Cmp40RegistryModel]::new()
$enum.Values['Service'] = 'nvlddmkm'
$enum.Values['Driver'] = '{4d36e968-e325-11ce-bfc1-08002be10318}\0001'
$root = [Cmp40RegistryRootModel]::new($enum)
Assert-Model ((Get-Cmp40DriverKey $root) -ceq $enum.Values['Driver']) 'Hardware-bound driver key selection'
$script:ModelDevices += [pscustomobject]@{ InstanceId = 'PCI\VEN_10DE&DEV_1F0B\SECOND_SYNTHETIC' }
Expect-ModelFailure { Get-Cmp40DriverKey $root } 'Exactly one'
$script:ModelDevices = @([pscustomobject]@{ InstanceId = 'PCI\VEN_10DE&DEV_1F0A\SYNTHETIC' })
Expect-ModelFailure { Get-Cmp40DriverKey $root } 'Exactly one'
$script:ModelDevices = @([pscustomobject]@{ InstanceId = 'PCI\VEN_10DE&DEV_1F0B\SYNTHETIC' })
$enum.Values['Service'] = 'other'
Expect-ModelFailure { Get-Cmp40DriverKey $root } 'not bound'
$enum.Values['Service'] = 'nvlddmkm'
$enum.Values['Driver'] = '{4d36e968-e325-11ce-bfc1-08002be10318}\0001\extra'
Expect-ModelFailure { Get-Cmp40DriverKey $root } 'Invalid.*driver key'

$snapshotKey = [Cmp40RegistryModel]::new()
$snapshotKey.SetValue('RMPcieLinkSpeed', [int32]::MinValue, [Microsoft.Win32.RegistryValueKind]::DWord)
$backup = [pscustomobject]@{
    Format = 'CMP40_GEN2_REGISTRY_V1'
    DriverKey = '{4d36e968-e325-11ce-bfc1-08002be10318}\0001'
    DriverVersion = '32.0.16.2114'
    Trial = 'Policy'
    Values = @(Get-Cmp40Entries $snapshotKey 'Policy')
}
$scratch = Join-Path $ScratchDirectory 'registry-model-backup.json'
$created = $false
try {
    Write-Cmp40Backup $backup $scratch
    $created = $true
    $loaded = [IO.File]::ReadAllText($scratch) | ConvertFrom-Json
    Assert-Cmp40Backup $loaded
    Assert-Model ($loaded.Values[0].Data -eq [int32]::MinValue) 'Backup signed DWORD round trip'
    $original = [IO.File]::ReadAllText($scratch)
    Expect-ModelFailure { Write-Cmp40Backup $backup $scratch } 'already exists'
    Assert-Model ([IO.File]::ReadAllText($scratch) -ceq $original) 'Existing backup preserved'
    $loaded.Values[0].Name = 'EnableGpuFirmware'
    Expect-ModelFailure { Assert-Cmp40Backup $loaded } 'value contract'
    $loaded.Values[0].Name = 'RMPcieLinkSpeed'
    $loaded.Values[0].Present = $true
    $loaded.Values[0].Data = [int64]::MaxValue
    Expect-ModelFailure { Assert-Cmp40Backup $loaded } 'original DWORD'
} finally {
    if ($created -and [IO.File]::Exists($scratch)) { [IO.File]::Delete($scratch) }
}
Write-Host 'PASS: registry trial model, rollback, ownership, types and exclusive backup.'

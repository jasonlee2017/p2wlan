param(
    [string]$Archive,
    [string]$InstallRoot = 'D:\Softwares\p2wlan',
    [string]$ConfigDir,
    [string]$DataDir,
    [switch]$Start,
    [switch]$Rollback
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Fail([string]$Message) {
    throw "p2wlan-server: $Message"
}

function Has-ServerConfig([string]$Path) {
    return (Test-Path -LiteralPath (Join-Path $Path 'control.env') -PathType Leaf) -and
        (Test-Path -LiteralPath (Join-Path $Path 'relay.env') -PathType Leaf)
}

function Read-EnvValue([string]$Path, [string]$Key) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return '' }
    foreach ($line in Get-Content -LiteralPath $Path) {
        if ($line -match "^\s*$Key\s*=\s*(.*)$") {
            $value = $Matches[1].Trim()
            if ($value.Length -ge 2 -and $value.StartsWith('"') -and $value.EndsWith('"')) {
                return $value.Substring(1, $value.Length - 2)
            }
            return $value
        }
    }
    return ''
}

if ([string]::IsNullOrWhiteSpace($ConfigDir)) {
    $configCandidates = @((Join-Path $InstallRoot 'config'), $InstallRoot)
    $ConfigDir = $configCandidates | Where-Object { Has-ServerConfig $_ } | Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($ConfigDir)) {
        Fail "control.env and relay.env were not found below $InstallRoot"
    }
}

if ([string]::IsNullOrWhiteSpace($DataDir)) {
    $configuredDb = Read-EnvValue (Join-Path $ConfigDir 'control.env') 'DB_PATH'
    if ($configuredDb -and [IO.Path]::IsPathRooted($configuredDb)) {
        $DataDir = Split-Path -Parent $configuredDb
    } else {
        $dataCandidates = @((Join-Path $InstallRoot 'data'), (Join-Path $ConfigDir 'data'), $InstallRoot)
        $relativeDb = if ($configuredDb) { $configuredDb } else { 'p2pnet.db' }
        $DataDir = $dataCandidates | Where-Object {
            Test-Path -LiteralPath (Join-Path $_ $relativeDb) -PathType Leaf
        } | Select-Object -First 1
        if ([string]::IsNullOrWhiteSpace($DataDir)) {
            $DataDir = $dataCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Container } | Select-Object -First 1
        }
        if ([string]::IsNullOrWhiteSpace($DataDir)) { $DataDir = Join-Path $InstallRoot 'data' }
    }
}

$scriptPath = Join-Path $PSScriptRoot 'Update-P2WlanServer.ps1'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) { Fail "generic updater not found: $scriptPath" }
$forward = @{ InstallRoot = $InstallRoot; ConfigDir = $ConfigDir; DataDir = $DataDir }
if ($Archive) { $forward.Archive = $Archive }
if ($Start) { $forward.Start = $true }
if ($Rollback) { $forward.Rollback = $true }
& $scriptPath @forward
if ($?) { exit 0 }
exit 1

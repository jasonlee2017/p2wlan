param(
    [ValidateSet('control', 'relay')]
    [string]$Role,
    [string]$ConfigDir = (Join-Path $env:ProgramData 'P2WLAN\config'),
    [string]$InstallRoot = $PSScriptRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Fail([string]$Message) {
    throw "p2wlan-server: $Message"
}

function Read-EnvFile([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Fail "configuration file not found: $Path"
    }
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path) {
        if ($line -match '^\s*([^#=\s][^=]*)=(.*)$') {
            $key = $Matches[1].Trim()
            $value = $Matches[2].Trim()
            if ($value.Length -ge 2 -and $value.StartsWith('"') -and $value.EndsWith('"')) {
                $value = $value.Substring(1, $value.Length - 2)
            }
            $values[$key] = $value
        }
    }
    return $values
}

$values = Read-EnvFile (Join-Path $ConfigDir "$Role.env")
foreach ($entry in $values.GetEnumerator()) {
    [Environment]::SetEnvironmentVariable([string]$entry.Key, [string]$entry.Value, 'Process')
}

$binary = Join-Path $InstallRoot "current\p2wlan-$Role.exe"
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    $binary = Join-Path $InstallRoot "p2wlan-$Role.exe"
}
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    Fail "service binary not found: $binary"
}

Set-Location -LiteralPath $InstallRoot
& $binary
$exitCode = $LASTEXITCODE
if ($null -eq $exitCode) { exit 0 }
exit $exitCode

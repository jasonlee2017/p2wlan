param(
    [string]$Archive,
    [string]$InstallRoot = (Join-Path $env:ProgramData 'P2Wlan\server'),
    [string]$ConfigDir = (Join-Path $env:ProgramData 'P2Wlan\config'),
    [string]$DataDir = (Join-Path $env:ProgramData 'P2Wlan\data'),
    [switch]$Start,
    [switch]$Rollback
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Fail([string]$Message) {
    throw "p2wlan-server: $Message"
}

function Ensure-Directory([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) {
        New-Item -ItemType Directory -Path $Path -Force | Out-Null
    }
}

function Test-PathWithin([string]$Path, [string]$Parent) {
    $pathFull = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    $parentFull = [IO.Path]::GetFullPath($Parent).TrimEnd('\')
    return $pathFull.Equals($parentFull, [StringComparison]::OrdinalIgnoreCase) -or
        $pathFull.StartsWith($parentFull + '\', [StringComparison]::OrdinalIgnoreCase)
}

function Copy-ConfigSnapshot([string]$Destination) {
    if (-not (Test-Path -LiteralPath $ConfigDir -PathType Container)) { return }
    Ensure-Directory $Destination
    foreach ($entry in @(Get-ChildItem -LiteralPath $ConfigDir -Force)) {
        if (Test-PathWithin $entry.FullName $DataDir) { continue }
        Copy-Item -LiteralPath $entry.FullName -Destination (Join-Path $Destination $entry.Name) -Recurse -Force
    }
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

function Set-ProcessEnvironment([hashtable]$Values) {
    foreach ($entry in $Values.GetEnumerator()) {
        [Environment]::SetEnvironmentVariable([string]$entry.Key, [string]$entry.Value, 'Process')
    }
}

$managedServiceNames = @('P2WLAN-Control', 'P2WLAN-Relay')

function Get-ManagedServices {
    $services = @()
    foreach ($name in $managedServiceNames) {
        $service = Get-Service -Name $name -ErrorAction SilentlyContinue
        if ($null -ne $service) {
            $services += $service
        }
    }
    return $services
}

function Wait-ServiceStatus([string]$Name, [string]$Expected, [int]$TimeoutSeconds = 30) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $service = Get-Service -Name $Name -ErrorAction SilentlyContinue
        if ($null -eq $service -or [string]$service.Status -eq $Expected) { return }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $deadline)
    Fail "service $Name did not reach state $Expected"
}

function Stop-ServerProcesses {
    foreach ($service in @(Get-ManagedServices)) {
        if ($service.Status -ne 'Stopped') {
            Write-Host "stopping service $($service.Name)"
            Stop-Service -Name $service.Name -Force
            Wait-ServiceStatus $service.Name 'Stopped'
        }
    }
    $names = @('p2wlan-control', 'p2wlan-relay')
    foreach ($name in $names) {
        foreach ($process in @(Get-Process -Name $name -ErrorAction SilentlyContinue)) {
            $path = $null
            try { $path = $process.Path } catch { }
            if ($path -and $path.StartsWith((Resolve-Path -LiteralPath $InstallRoot).Path, [StringComparison]::OrdinalIgnoreCase)) {
                Write-Host "stopping $name pid=$($process.Id)"
                Stop-Process -Id $process.Id -Force
                try { $process.WaitForExit(15000) } catch { }
            }
        }
    }
}

function Resolve-DatabasePath([hashtable]$ControlEnv) {
    $configured = [string]$ControlEnv['DB_PATH']
    if ([string]::IsNullOrWhiteSpace($configured)) {
        return Join-Path $DataDir 'p2pnet.db'
    }
    if ([IO.Path]::IsPathRooted($configured)) { return $configured }
    return Join-Path $DataDir $configured
}

function Resolve-DatabaseTool {
    $candidates = @(
        (Join-Path $InstallRoot 'current\p2wlan-db.exe')
        (Join-Path $InstallRoot 'p2wlan-db.exe')
    )
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    Fail "database snapshot tool not found below $InstallRoot"
}

function New-ServerBackup([hashtable]$ControlEnv) {
    Ensure-Directory $DataDir
    $backupRoot = Join-Path $DataDir 'backups'
    Ensure-Directory $backupRoot
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
    $backup = Join-Path $backupRoot "p2wlan-$stamp"
    Ensure-Directory $backup
    $database = Resolve-DatabasePath $ControlEnv
    if (Test-Path -LiteralPath $database -PathType Leaf) {
        $snapshot = Join-Path $backup 'database.sqlite'
        $dbTool = Resolve-DatabaseTool
        & $dbTool --source $database --output $snapshot
        if ($LASTEXITCODE -ne 0) { Fail "database snapshot failed" }
        Get-FileHash -LiteralPath $snapshot -Algorithm SHA256 | Format-List | Out-File -LiteralPath (Join-Path $backup 'database.sha256') -Encoding ascii
    }
    Copy-ConfigSnapshot (Join-Path $backup 'config')
    $currentPath = Join-Path $InstallRoot 'current'
    $currentName = if (Test-Path -LiteralPath $currentPath -PathType Container) {
        (Get-Item -LiteralPath $currentPath).Name
    } else {
        ''
    }
    @(
        "created_at=$([DateTime]::UtcNow.ToString('o'))"
        "install_root=$InstallRoot"
        "config_dir=$ConfigDir"
        "data_dir=$DataDir"
        "current=$currentName"
    ) | Set-Content -LiteralPath (Join-Path $backup 'metadata.txt') -Encoding ascii
    Write-Host "backup=$backup"
    return $backup
}

function Test-Archive([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { Fail "archive not found: $Path" }
    $checksumPath = "$Path.sha256"
    if (-not (Test-Path -LiteralPath $checksumPath -PathType Leaf)) { Fail "checksum not found: $checksumPath" }
    $expected = ((Get-Content -LiteralPath $checksumPath | Select-Object -First 1) -split '\s+')[0].Trim().ToLowerInvariant()
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expected -ne $actual) { Fail "archive checksum mismatch: expected=$expected actual=$actual" }
}

function Get-ArchiveVersion([string]$Path) {
    $probe = Join-Path ([IO.Path]::GetTempPath()) ("p2wlan-probe-" + [Guid]::NewGuid().ToString('N'))
    try {
        Expand-Archive -LiteralPath $Path -DestinationPath $probe -Force
        $metadataPath = Join-Path $probe 'BUILD-METADATA.txt'
        if (-not (Test-Path -LiteralPath $metadataPath -PathType Leaf)) { Fail 'archive is missing BUILD-METADATA.txt' }
        $versionLine = Get-Content -LiteralPath $metadataPath | Where-Object { $_ -match '^version=' } | Select-Object -First 1
        $commitLine = Get-Content -LiteralPath $metadataPath | Where-Object { $_ -match '^commit=' } | Select-Object -First 1
        $version = if ($versionLine) { ($versionLine -split '=', 2)[1].Trim() } else { '' }
        $commit = if ($commitLine) { ($commitLine -split '=', 2)[1].Trim() } else { '' }
        if ($version -notmatch '^server-[A-Za-z0-9._-]+$' -or $commit -notmatch '^[0-9a-f]{40}$') { Fail 'archive metadata has an invalid version or commit' }
        foreach ($name in @('p2wlan-control.exe', 'p2wlan-relay.exe', 'p2wlan-config.exe', 'p2wlan-db.exe', 'Update-P2WlanServer.ps1', 'Run-P2WlanRole.ps1')) {
            if (-not (Test-Path -LiteralPath (Join-Path $probe $name) -PathType Leaf)) { Fail "archive is missing $name" }
        }
        return $version
    } finally {
        Remove-Item -LiteralPath $probe -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Install-ServiceLauncher {
    $current = Join-Path $InstallRoot 'current'
    $candidates = @(
        (Join-Path $PSScriptRoot 'Run-P2WlanRole.ps1')
        (Join-Path $current 'Run-P2WlanRole.ps1')
    )
    $source = $candidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    if ($source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $InstallRoot 'Run-P2WlanRole.ps1') -Force
        Write-Host "installed service launcher=$source"
    } elseif ((Get-ManagedServices).Count -gt 0) {
        Fail 'NSSM services require Run-P2WlanRole.ps1'
    }
}

function Start-ServerProcesses([hashtable]$ControlEnv, [hashtable]$RelayEnv) {
    $services = @(Get-ManagedServices)
    if ($services.Count -gt 0) {
        foreach ($service in $services) {
            if ($service.Status -ne 'Running') {
                Write-Host "starting service $($service.Name)"
                Start-Service -Name $service.Name
                Wait-ServiceStatus $service.Name 'Running'
            }
        }
        return
    }
    Ensure-Directory $DataDir
    $logs = Join-Path $DataDir 'logs'
    Ensure-Directory $logs
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
    Set-ProcessEnvironment $ControlEnv
    $control = Start-Process -FilePath (Join-Path $InstallRoot 'current\p2wlan-control.exe') -WorkingDirectory $DataDir -RedirectStandardOutput (Join-Path $logs "control-$stamp.log") -RedirectStandardError (Join-Path $logs "control-$stamp.err") -PassThru
    Set-ProcessEnvironment $RelayEnv
    $relay = Start-Process -FilePath (Join-Path $InstallRoot 'current\p2wlan-relay.exe') -WorkingDirectory $DataDir -RedirectStandardOutput (Join-Path $logs "relay-$stamp.log") -RedirectStandardError (Join-Path $logs "relay-$stamp.err") -PassThru
    @("control=$($control.Id)", "relay=$($relay.Id)") | Set-Content -LiteralPath (Join-Path $InstallRoot 'pids.txt') -Encoding ascii
    Write-Host "started control pid=$($control.Id), relay pid=$($relay.Id)"
}

function Test-ServerHealth([hashtable]$ControlEnv) {
    $port = [int]$ControlEnv['PORT']
    $uri = "http://127.0.0.1:$port/health"
    $deadline = (Get-Date).AddSeconds(30)
    do {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 3
            if ($response.StatusCode -eq 200 -and $response.Content.Trim() -eq 'ok') { return }
        } catch { Start-Sleep -Milliseconds 500 }
    } while ((Get-Date) -lt $deadline)
    Fail "Control health check failed: $uri"
}

function Switch-Release([string]$Version, [string]$Stage) {
    Ensure-Directory (Join-Path $InstallRoot 'releases')
    $release = Join-Path $InstallRoot "releases\$Version"
    if (Test-Path -LiteralPath $release) { Remove-Item -LiteralPath $release -Recurse -Force }
    Move-Item -LiteralPath $Stage -Destination $release
    $current = Join-Path $InstallRoot 'current'
    $previous = Join-Path $InstallRoot 'previous'
    if (Test-Path -LiteralPath $previous) { Remove-Item -LiteralPath $previous -Recurse -Force }
    if (Test-Path -LiteralPath $current) { Move-Item -LiteralPath $current -Destination $previous }
    Move-Item -LiteralPath $release -Destination $current
    Set-Content -LiteralPath (Join-Path $InstallRoot 'version.txt') -Value $Version -Encoding ascii
}

function Rollback-Server {
    $current = Join-Path $InstallRoot 'current'
    $previous = Join-Path $InstallRoot 'previous'
    if (-not (Test-Path -LiteralPath $previous -PathType Container)) { Fail 'no previous release is available' }
    Stop-ServerProcesses
    $failed = Join-Path $InstallRoot ("failed-" + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ'))
    if (Test-Path -LiteralPath $current) { Move-Item -LiteralPath $current -Destination $failed }
    Move-Item -LiteralPath $previous -Destination $current
    Install-ServiceLauncher
    $metadata = Join-Path $current 'BUILD-METADATA.txt'
    $versionLine = Get-Content -LiteralPath $metadata | Where-Object { $_ -match '^version=' } | Select-Object -First 1
    Set-Content -LiteralPath (Join-Path $InstallRoot 'version.txt') -Value (($versionLine -split '=', 2)[1].Trim()) -Encoding ascii
    if ($Start) {
        $controlEnv = Read-EnvFile (Join-Path $ConfigDir 'control.env')
        $relayEnv = Read-EnvFile (Join-Path $ConfigDir 'relay.env')
        Start-ServerProcesses $controlEnv $relayEnv
        Test-ServerHealth $controlEnv
    }
}

if ($Rollback) {
    Rollback-Server
    exit 0
}

if ([string]::IsNullOrWhiteSpace($Archive)) { Fail '-Archive is required unless -Rollback is used' }
$Archive = (Resolve-Path -LiteralPath $Archive).Path
$InstallRoot = [IO.Path]::GetFullPath($InstallRoot)
$ConfigDir = [IO.Path]::GetFullPath($ConfigDir)
$DataDir = [IO.Path]::GetFullPath($DataDir)
Ensure-Directory $InstallRoot
Ensure-Directory $ConfigDir
Ensure-Directory $DataDir
$controlEnv = Read-EnvFile (Join-Path $ConfigDir 'control.env')
$relayEnv = Read-EnvFile (Join-Path $ConfigDir 'relay.env')
Test-Archive $Archive
$version = Get-ArchiveVersion $Archive
$backup = $null
try {
    Stop-ServerProcesses
    $backup = New-ServerBackup $controlEnv
    $stage = Join-Path $InstallRoot ("stage-" + [Guid]::NewGuid().ToString('N'))
    Expand-Archive -LiteralPath $Archive -DestinationPath $stage -Force
    Switch-Release $version $stage
    Install-ServiceLauncher
    if ($Start) {
        Start-ServerProcesses $controlEnv $relayEnv
        Test-ServerHealth $controlEnv
    }
    Write-Host "updated version=$version"
    Write-Host "backup=$backup"
} catch {
    Write-Error $_
    if ($backup) { Write-Host "update failed; backup remains at $backup" }
    exit 1
}

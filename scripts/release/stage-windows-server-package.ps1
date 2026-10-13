param(
    [Parameter(Mandatory = $true)]
    [string]$Archive,
    [string]$RepositoryRoot,
    [string]$OutputRoot,
    [string]$ReleaseDate,
    [string]$Version
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Stop-Stage([string]$Message) {
    throw "Windows 服务端发布整理失败：$Message"
}

if (-not $RepositoryRoot) {
    $RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
} else {
    $RepositoryRoot = (Resolve-Path -LiteralPath $RepositoryRoot).Path
}
$Archive = (Resolve-Path -LiteralPath $Archive).Path
$checksumPath = "$Archive.sha256"
if (-not (Test-Path -LiteralPath $checksumPath -PathType Leaf)) {
    Stop-Stage "缺少校验文件：$checksumPath"
}
$expectedHash = ((Get-Content -LiteralPath $checksumPath | Select-Object -First 1) -split '\s+')[0].Trim().ToLowerInvariant()
$actualHash = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($expectedHash -ne $actualHash) {
    Stop-Stage "归档校验失败：expected=$expectedHash actual=$actualHash"
}

Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::OpenRead($Archive)
try {
    $metadataEntry = $zip.Entries | Where-Object { $_.FullName -eq 'BUILD-METADATA.txt' } | Select-Object -First 1
    if ($null -eq $metadataEntry) { Stop-Stage '归档缺少 BUILD-METADATA.txt' }
    $reader = New-Object System.IO.StreamReader($metadataEntry.Open())
    try { $metadata = $reader.ReadToEnd() } finally { $reader.Dispose() }
} finally {
    $zip.Dispose()
}
$metadataVersion = (($metadata -split "`r?`n") | Where-Object { $_ -match '^version=' } | Select-Object -First 1) -replace '^version=', ''
$metadataVersion = $metadataVersion.Trim()
if ($metadataVersion -notmatch '^server-[A-Za-z0-9._-]+$') {
    Stop-Stage "BUILD-METADATA.txt 中的 version 无效：$metadataVersion"
}
if ($Version -and $Version -ne $metadataVersion) {
    Stop-Stage "指定版本与归档版本不一致：$Version / $metadataVersion"
}
$Version = $metadataVersion
if (-not $ReleaseDate) { $ReleaseDate = (Get-Date).ToString('yyyy-MM-dd') }
if ($ReleaseDate -notmatch '^\d{4}-\d{2}-\d{2}$') {
    Stop-Stage "ReleaseDate 必须使用 yyyy-MM-dd 格式：$ReleaseDate"
}
if (-not $OutputRoot) { $OutputRoot = Join-Path $RepositoryRoot 'release\server' }
$OutputRoot = [System.IO.Path]::GetFullPath($OutputRoot)
$destination = Join-Path $OutputRoot "windows-amd64\$ReleaseDate\$Version"
New-Item -ItemType Directory -Path $destination -Force | Out-Null

$archiveName = Split-Path $Archive -Leaf
Copy-Item -LiteralPath $Archive -Destination (Join-Path $destination $archiveName) -Force
Copy-Item -LiteralPath $checksumPath -Destination (Join-Path $destination (Split-Path $checksumPath -Leaf)) -Force
$sourceFiles = @(
    @{ Source = Join-Path $RepositoryRoot 'scripts\Update-P2WlanServer.ps1'; Name = 'Update-P2WlanServer.ps1' },
    @{ Source = Join-Path $RepositoryRoot 'scripts\Run-P2WlanRole.ps1'; Name = 'Run-P2WlanRole.ps1' }
)
$sourceFiles += @{ Source = Join-Path $RepositoryRoot 'scripts\Update-P2WlanServer-D-Softwares.ps1'; Name = 'Update-P2WlanServer-D-Softwares.ps1' }
foreach ($file in $sourceFiles) {
    if (Test-Path -LiteralPath $file.Source -PathType Leaf) {
        Copy-Item -LiteralPath $file.Source -Destination (Join-Path $destination $file.Name) -Force
    }
}
$instructions = Join-Path $RepositoryRoot 'scripts\release\WINDOWS-UPDATE-D-Softwares.txt'
if (Test-Path -LiteralPath $instructions -PathType Leaf) {
    Copy-Item -LiteralPath $instructions -Destination (Join-Path $destination 'WINDOWS-UPDATE-D-Softwares.txt') -Force
}
@(
    "version=$Version"
    "release_date=$ReleaseDate"
    "archive=$archiveName"
    "sha256=$actualHash"
) | Set-Content -LiteralPath (Join-Path $destination 'RELEASE-METADATA.txt') -Encoding ascii
Write-Host "服务端发布目录=$destination"
Write-Host "归档=$archiveName"
Write-Host "sha256=$actualHash"

param(
    [string]$RepositoryRoot,
    [string]$OutputDirectory,
    [switch]$BuildInstaller
)

$ErrorActionPreference = 'Stop'

function Stop-Build([string]$Message) {
    throw "Windows 客户端打包失败：$Message"
}

if (-not $RepositoryRoot) {
    $RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
} else {
    $RepositoryRoot = (Resolve-Path -LiteralPath $RepositoryRoot).Path
}

if (-not $OutputDirectory) {
    $OutputDirectory = Join-Path $RepositoryRoot 'dist-release\windows-client-local'
}
$OutputDirectory = [System.IO.Path]::GetFullPath($OutputDirectory)

foreach ($command in @('git', 'cargo', 'python3', 'flutter')) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        Stop-Build "找不到 $command。请先安装对应工具，并确认它已加入 PATH。"
    }
}

$bash = Get-Command bash -ErrorAction SilentlyContinue
if (-not $bash) {
    Stop-Build '找不到 bash。请安装 Git for Windows；仓库的客户端身份构建脚本通过 Git Bash 执行。'
}

$flutterDirectory = Join-Path $RepositoryRoot 'apps\flutter_client'
$daemonProject = Join-Path $RepositoryRoot 'client\daemon\Cargo.toml'
if (-not (Test-Path -LiteralPath $flutterDirectory)) {
    Stop-Build "客户端目录不存在：$flutterDirectory"
}
if (-not (Test-Path -LiteralPath $daemonProject)) {
    Stop-Build "daemon 工程不存在：$daemonProject"
}

Push-Location $RepositoryRoot
try {
    Write-Host '启用 Flutter Windows 桌面构建...'
    & flutter config --enable-windows-desktop
    if ($LASTEXITCODE -ne 0) { Stop-Build 'flutter config 执行失败。' }

    Write-Host '安装 Dart 依赖...'
    Push-Location $flutterDirectory
    try {
        & flutter pub get
        if ($LASTEXITCODE -ne 0) { Stop-Build 'flutter pub get 执行失败。' }
    } finally {
        Pop-Location
    }

    Write-Host '编译 release daemon...'
    & cargo build -p p2wlan-daemon --release
    if ($LASTEXITCODE -ne 0) { Stop-Build 'cargo build -p p2wlan-daemon --release 执行失败。' }

    Write-Host '编译 Flutter Windows 客户端并写入源码身份...'
    & $bash.Source 'scripts/release/build_flutter_client.sh' windows --release
    if ($LASTEXITCODE -ne 0) { Stop-Build 'Flutter Windows release 构建失败。' }

    $releaseDirectory = Get-ChildItem -LiteralPath (Join-Path $flutterDirectory 'build\windows') -Directory -Recurse |
        Where-Object { $_.FullName -match '\\runner\\Release$' } |
        Select-Object -First 1
    if (-not $releaseDirectory) {
        Stop-Build '没有找到 Flutter Windows Release 目录。'
    }

    $daemon = Join-Path $RepositoryRoot 'target\release\p2wlan-daemon.exe'
    if (-not (Test-Path -LiteralPath $daemon)) {
        Stop-Build "没有找到 daemon：$daemon"
    }

    $portableDirectory = Join-Path $OutputDirectory 'p2wlan-windows-x64-portable'
    if (Test-Path -LiteralPath $OutputDirectory) {
        Remove-Item -LiteralPath $OutputDirectory -Recurse -Force
    }
    New-Item -ItemType Directory -Path $portableDirectory -Force | Out-Null
    Copy-Item -Path (Join-Path $releaseDirectory.FullName '*') -Destination $portableDirectory -Recurse -Force
    Copy-Item -LiteralPath $daemon -Destination (Join-Path $portableDirectory 'p2wlan-daemon.exe') -Force

    Write-Host '下载并校验 Wintun...'
    & (Join-Path $flutterDirectory 'scripts\fetch_wintun.ps1') -DestinationDirectory $portableDirectory -Architecture amd64
    if ($LASTEXITCODE -ne 0) { Stop-Build 'Wintun 准备失败。' }

    Write-Host '捆绑 MSVC 运行库...'
    & (Join-Path $flutterDirectory 'scripts\bundle_msvc_runtime.ps1') -DestinationDirectory $portableDirectory
    if ($LASTEXITCODE -ne 0) { Stop-Build 'MSVC 运行库准备失败。' }

    $requiredFiles = @(
        'p2wlan_flutter_client.exe',
        'p2wlan-daemon.exe',
        'wintun.dll',
        'vcruntime140.dll',
        'vcruntime140_1.dll',
        'msvcp140.dll'
    )
    foreach ($file in $requiredFiles) {
        if (-not (Test-Path -LiteralPath (Join-Path $portableDirectory $file))) {
            Stop-Build "portable 包缺少 $file。"
        }
    }

    $identityPath = Join-Path $portableDirectory 'build-identity.json'
    $expectedCommit = (& git rev-parse HEAD).Trim()
    & python3 (Join-Path $RepositoryRoot 'scripts\release\verify_release_identity.py') `
        --daemon (Join-Path $portableDirectory 'p2wlan-daemon.exe') `
        --expected-commit $expectedCommit | Set-Content -LiteralPath $identityPath -Encoding UTF8
    if ($LASTEXITCODE -ne 0) { Stop-Build '客户端与 daemon 版本身份校验失败。' }

    $portableZip = Join-Path $OutputDirectory 'p2wlan-windows-x64-portable.zip'
    Compress-Archive -Path (Join-Path $portableDirectory '*') -DestinationPath $portableZip -CompressionLevel Optimal

    if ($BuildInstaller) {
        $isccCandidates = @()
        $programFilesX86 = [Environment]::GetEnvironmentVariable('ProgramFiles(x86)')
        foreach ($base in @($programFilesX86, $env:ProgramFiles)) {
            if ($base) { $isccCandidates += Join-Path $base 'Inno Setup 6\ISCC.exe' }
        }
        if ($env:ChocolateyInstall) {
            $isccCandidates += Join-Path $env:ChocolateyInstall 'lib\innosetup\tools\ISCC.exe'
        }
        $iscc = $isccCandidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
        if (-not $iscc) {
            Stop-Build '未找到 Inno Setup 6。安装 Inno Setup 后重新运行，或省略 -BuildInstaller 先测试 portable 包。'
        }

        $version = (Select-String -LiteralPath (Join-Path $flutterDirectory 'pubspec.yaml') -Pattern '^version:\s*([^+\s]+)').Matches[0].Groups[1].Value
        $iss = Join-Path $env:TEMP 'p2wlan-windows-local.iss'
        $source = $portableDirectory.Replace('"', '""')
        $output = $OutputDirectory.Replace('"', '""')
        @"
#define SourceDir "$source"
#define OutputDir "$output"
#define AppVersion "$version"

[Setup]
AppId={{D2C22E34-410C-4273-B888-0E689638C994}
AppName=P2WLAN
AppVersion={#AppVersion}
AppPublisher=P2WLAN
DefaultDirName={localappdata}\Programs\P2WLAN
DefaultGroupName=P2WLAN
DisableProgramGroupPage=yes
OutputDir={#OutputDir}
OutputBaseFilename=p2wlan-windows-x64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=lowest
UninstallDisplayIcon={app}\p2wlan_flutter_client.exe

[Files]
Source: "{#SourceDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\P2WLAN"; Filename: "{app}\p2wlan_flutter_client.exe"
Name: "{autodesktop}\P2WLAN"; Filename: "{app}\p2wlan_flutter_client.exe"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional icons:"; Flags: unchecked

[Run]
Filename: "{app}\p2wlan_flutter_client.exe"; Description: "Launch P2WLAN"; Flags: nowait postinstall skipifsilent
"@ | Set-Content -LiteralPath $iss -Encoding UTF8
        & $iscc $iss
        if ($LASTEXITCODE -ne 0) { Stop-Build 'Inno Setup 构建失败。' }
    }

    Write-Host "打包完成：$OutputDirectory"
} finally {
    Pop-Location
}

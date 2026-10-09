# Windows 客户端独立打包

此目录用于在另一台 Windows x64 电脑上重新构建 P2WLAN 客户端，验证 Windows daemon 启动超时修复。它不包含 Flutter SDK、Rust target、缓存、账号配置、证书或任何运行时凭据。

## 准备环境

在打包电脑安装并加入 PATH：

- Flutter 3.47.2，Dart 3.13.2；仓库根目录的 `.fvmrc` 是版本依据；
- Rust stable 和 MSVC 工具链；
- Git for Windows，提供 `bash.exe`；
- Python 3；
- Visual Studio Build Tools 的 C++ 桌面组件；`bundle_msvc_runtime.ps1` 会从 Visual Studio 复制运行库；
- 如果要生成安装程序，再安装 Inno Setup 6。

打包电脑不需要安装或运行 Control、Relay，也不需要复制云服务器证书。构建时需要联网下载 Dart 依赖和经过 SHA-256 校验的 Wintun 0.14.1 压缩包。

## 获取源码

推荐在 PowerShell 中执行：

```powershell
git clone https://github.com/yhan-sun/p2wlan.git
cd p2wlan
```

如果使用包含当前修复的分支，则先切换到该分支，再执行打包脚本。

## 生成 portable 测试包

在仓库根目录执行：

```powershell
.\deploy\windows-client-build\Build-P2WlanWindowsClient.ps1
```

脚本会依次完成 Flutter Windows 配置、Dart 依赖安装、Rust daemon release 编译、Flutter 客户端 release 编译、Wintun 校验、MSVC 运行库捆绑和客户端/daemon 版本身份校验。

输出目录：

```text
dist-release\windows-client-local\p2wlan-windows-x64-portable\
dist-release\windows-client-local\p2wlan-windows-x64-portable.zip
```

portable 目录或 ZIP 中的 `p2wlan_flutter_client.exe` 可以直接用于测试。客户端和 daemon 必须来自同一次构建，不能把旧版本的 `p2wlan-daemon.exe` 单独替换进去。

## 生成安装程序

安装 Inno Setup 6 后执行：

```powershell
.\deploy\windows-client-build\Build-P2WlanWindowsClient.ps1 -BuildInstaller
```

额外输出：

```text
dist-release\windows-client-local\p2wlan-windows-x64-setup.exe
```

安装程序使用当前用户目录，不需要管理员权限。首次连接房间时，客户端可能请求管理员权限，以便创建 Wintun 网卡和安装路由。

## 验证启动超时修复

安装或运行新客户端后，连接房间并等待约 45 秒。成功时应同时满足：

```powershell
Get-CimInstance Win32_Process -Filter "Name = 'p2wlan-daemon.exe'" | Select-Object ProcessId,CommandLine
Get-NetAdapter | Where-Object Name -like 'p2r*'
Get-NetRoute -DestinationPrefix '10.21.1.0/24'
```

客户端与 daemon 的构建身份保存在 portable 包的 `build-identity.json`。若客户端仍显示 `STARTUP_TIMEOUT`，请保留对应房间目录下的 `p2wlan-client.log` 和 `p2wlan-daemon.log` 尾部，先不要删除日志目录。

## 说明

当前修复把 Windows daemon 诊断端点等待窗口从 20 秒提高到 45 秒。你提供的日志中 daemon 在约 27 秒后才进入 `[startup] ready`，因此旧客户端会在 daemon 最终成功前误判超时。

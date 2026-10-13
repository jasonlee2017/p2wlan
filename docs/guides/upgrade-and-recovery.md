# 升级与恢复

## 升级

每次升级只使用一个固定 server-vX.Y.Z 归档：

    sudo p2wlan-server backup
    sudo p2wlan-server update --version server-vX.Y.Z
    sudo p2wlan-server verify --service all
    sudo p2wlan-server check --service all
    sudo p2wlan-server doctor --service all

Windows x64 使用同一版本标签下的 `p2wlan-server-windows-amd64.zip`。本地发布物固定放在 `release/server/windows-amd64/<yyyy-MM-dd>/<server-version>/`，归档、同名 `.sha256` 和更新说明放在同一版本目录。客户端 Windows 发布物固定放在 `release/client/windows/<yyyy-MM-dd>/<client-version>/`。不要从 `release/` 根目录或测试目录选择归档。

整理服务端归档时，在仓库根目录执行：

    .\scripts\release\stage-windows-server-package.ps1 -Archive .\release\incoming\p2wlan-server-windows-amd64.zip

先把待整理归档和同名 `.sha256` 放入 `release/incoming/`；脚本会先校验同目录 `.sha256`，再读取归档内的 `BUILD-METADATA.txt`，按日期和版本创建固定目录并复制归档、校验文件和更新脚本。正式部署只使用该脚本输出的版本目录。

在云服务器上，把该版本目录中的归档、`.sha256` 和 `Update-P2WlanServer-D-Softwares.ps1` 一起复制到部署者指定的固定临时目录 `<windows-update-directory>`。在管理员 PowerShell 中，先确认 `.sha256` 与归档位于同一目录，再运行：

    .\Update-P2WlanServer.ps1 -Archive .\p2wlan-server-windows-amd64.zip -Start

对于使用固定 D 盘安装根目录的 Windows 部署，使用归档中的 `Update-P2WlanServer-D-Softwares.ps1` 包装脚本。把归档和 `.sha256` 文件放在同一个临时目录，在该目录打开管理员 PowerShell 后逐条执行以下步骤；不要把 PowerShell 提示符一起复制进去：

    $Archive = Join-Path (Get-Location) 'p2wlan-server-windows-amd64.zip'
    $ConfigDir = Join-Path $env:ProgramData 'P2WLAN\config'
    Get-FileHash -LiteralPath $Archive -Algorithm SHA256
    Expand-Archive -LiteralPath $Archive -DestinationPath (Get-Location) -Force
    Set-ExecutionPolicy -Scope Process Bypass -Force
    & '.\Update-P2WlanServer-D-Softwares.ps1' -Archive $Archive -ConfigDir $ConfigDir -Start

`Get-FileHash` 的结果必须与同目录 `.sha256` 文件中的摘要完全一致；摘要不一致时停止，不要继续解压或更新。`Set-ExecutionPolicy -Scope Process` 只影响当前 PowerShell 窗口。D 盘包装脚本默认使用部署者约定的安装根目录；如果配置不在默认位置，显式传入 `-ConfigDir` 和 `-DataDir`。更新过程会停止 `P2WLAN-Control` 和 `P2WLAN-Relay`，创建 SQLite 一致性备份，切换 `current` 发布目录，再启动服务并检查 Control 健康状态。

更新成功时应看到 `backup=...` 和 `updated version=...`。随后逐条确认服务、进程路径和版本：

    Get-Service -Name 'P2WLAN-Control','P2WLAN-Relay' | Select-Object Name,Status
    Get-Process -Name 'p2wlan-control','p2wlan-relay' | Select-Object Id,Path
    Get-Content -LiteralPath '<windows-install-root>\version.txt'

两个服务应为 `Running`，两个进程的路径应位于安装根目录的 `current` 子目录；版本文件位于安装根目录的 `version.txt`。若更新中途失败，先保留 `data\backups` 中的备份并记录错误，不要连续重复执行旧归档；确认服务停止原因后再使用同版本的修复归档重试。

脚本会在停止进程或受管理服务后生成数据库一致性快照，并把快照和配置保存在数据目录的 `backups` 下。更新后的前一版本保留在安装目录的 `previous`，可用以下命令回滚并重新启动：

    .\Update-P2WlanServer.ps1 -Rollback -Start

升级保留已有配置和数据。当前 manager 在激活新发布前检查支持日志配置：`LOG_UPLOAD_DIR` 缺失或为空时，仅追加数据目录下 `log-uploads` 的绝对路径；非空自定义值保持原样。它只创建或恢复受管理的最后一级目录，不递归修改数据文件；符号链接、其他用户属主、配置锁冲突或配置在检查期间被替换时拒绝继续。准备过程只解析完整的单行环境赋值；任意变量使用多行引号或反斜线续行时，报告 `unsupported_environment_syntax` 并在更改目录和配置前停止，不把其他变量中的文本误作配置项。准备失败不会切换当前发布。更新前后记录准确版本、源码提交、归档 checksum、服务健康检查和 doctor 结果。doctor 的主机级检查仍不替代真实客户端/TUN/公网入口验收。

旧版 manager 自身不具备新增的准备逻辑。升级遗留安装时，先从已校验的新归档安装其中的 `p2wlan-server`，再运行该版本的 update；上传部署入口会在 update 前安装同归档的 manager。已有自定义日志目录不会被自动接管，须由部署者按原路径核对访问权限。该补齐过程不会轮换 JWT、管理台、Relay 或 TLS 凭据。

## 备份

backup 使用服务端内置的 SQLite 一致性快照工具，不按顺序复制在线的主文件、WAL 和 SHM。数据库路径读取 Control 的 DB_PATH；相对路径相对于服务数据目录。备份目录包含数据库摘要、完整性检查结果、schema 版本和当前发布元数据。

配置含有秘密。需要把配置一起保存时，使用独立于备份包的 age 私钥：

    sudo p2wlan-server backup --include-config --age-recipient age1example

age 私钥不放在服务器备份目录、同一个归档或命令行中。没有加密接收方时，backup 只保存数据库快照和恢复所需的非秘密元数据。

## 恢复

恢复要求 systemd 可用，并在替换数据库前验证 checksum、SQLite integrity_check 和备份元数据：

    sudo p2wlan-server restore --backup /var/lib/p2wlan/backups/p2wlan-YYYYMMDDTHHMMSSZ

如果备份包含加密配置，显式提供离线保存的 age identity：

    sudo p2wlan-server restore \
      --backup /var/lib/p2wlan/backups/p2wlan-YYYYMMDDTHHMMSSZ \
      --age-identity /secure/recovery-key.txt

恢复前会为当前数据库创建一致性 pre-restore 快照，并保存现有 Control/Relay 配置和两项服务的运行状态。服务原本运行时会在替换数据前严格停止对应服务；停止失败立即中止。恢复后的启动或健康检查失败时，manager 会恢复 pre-restore 数据库、原配置和原服务状态；如果这一步也无法完整完成，会保留 recovery copy 并明确报错，不会把部分恢复状态报告为成功。服务原本未运行时不会擅自启动。恢复完成后仍须执行登录、房间、票据、撤权和双端通信验证。

## 回滚

    sudo p2wlan-server rollback --version server-vX.Y.Z
    sudo p2wlan-server check --service all

回滚只切换到已经安装的发布目录，并检查版本和健康状态，不自动把新 schema 降回旧 schema。新版本写入数据后，旧版本可能不兼容；此时应恢复兼容的数据库快照或向前修复，不应承诺无损自动降级。

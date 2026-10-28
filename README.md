# betterSSH

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](#依赖)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS-2E7D32)](#依赖)
[![TUI](https://img.shields.io/badge/TUI-Bubble%20Tea-7D4CDB)](https://github.com/charmbracelet/bubbletea)

`betterSSH` 是一个合法、安全的 SSH 连接与配置管理工具。它以 `~/.ssh/config` 为唯一 Host 数据源，提供 TUI 和 CLI，支持模糊搜索、块级非破坏性编辑、sshfs 挂载后启动原生 `ssh`，以及通过操作系统钥匙串 + OpenSSH AskPass 自动填充密码。

它不实现 SSH 协议，不调用 `sshpass`，不提供暴力破解、未授权访问或任何攻击能力。

> 本文档中的主机名、用户名、IP 和路径均为虚构示例（如 `example.com`、`alice`），不对应任何真实环境。上传到 GitHub 前请再次检查自己的截图与示例。

```
┌ betterSSH  ~/.ssh/config  2 hosts ─────────────────────────────────────┐
│ 挂载点: ~/sshfs/prod  ←  prod:/   (m 挂载并连接)                        │
│   ALIAS    HOSTNAME                 USER    PORT  IDENTITYFILE         │
│ > prod     prod.example.com         alice   22    ~/.ssh/id_ed25519     │
│   staging  staging.example.com      deploy  2222  ~/.ssh/id_staging     │
└────────────────────────────────────────────────────────────────────────┘
```

## 特性

- 列出、模糊搜索、新建、编辑、删除 `~/.ssh/config` 中的 Host。
- 删除 Host 时自动删除其对应的系统钥匙串密码。
- 写回采用块级更新，保留注释、空行、顺序、未知字段和未管理字段。
- 写回前生成 `config.bak`，使用同目录临时文件 + 原子替换；检测到文件被其他程序修改时拒绝覆盖。
- 编辑字段：`HostName`、`User`、`Port`、`IdentityFile`、`ProxyJump`、`ServerAliveInterval`。
- 主界面显示选中 Host 的本机挂载点和远程路径。
- sshfs 挂载后启动原生 `ssh <alias>`；支持严格挂载模式、已挂载处理、手动/自动卸载。
- SSH 退出后询问“继续保留挂载 / 卸载并释放”；可通过设置自动卸载。
- 密码只保存到系统钥匙串，连接时通过 OpenSSH AskPass 自动填充。
- 提供 TUI 和无需进入 TUI 的 CLI 子命令。
- 设置独立保存在 `~/.config/betterSSH/settings.json`，不污染 SSH 配置。
- 不重新实现 SSH 客户端，不使用 `sshpass`，不把密码写入 argv、环境变量、配置文件或日志。

## 适合谁

- 经常维护多台服务器，希望快速浏览、编辑 `~/.ssh/config` 的人。
- 习惯把远程目录通过 sshfs 挂载到本机，再使用原生 `ssh` 的人。
- 希望在 TUI 之外，用脚本完成新增、修改、删除、查看、挂载和卸载的人。
- 不希望把密码明文保存到配置文件或数据库的人。

## 依赖

| 依赖 | 必需性 | 说明 |
|---|---|---|
| Go 1.22+ | 构建 | 编译单个二进制 |
| OpenSSH 客户端 | 必需 | 使用系统 `ssh`；AskPass 自动填充需要 OpenSSH 8.4+ |
| sshfs | 挂载时必需 | Linux 安装 sshfs；macOS 安装 macFUSE + sshfs |
| FUSE | 挂载时必需 | Linux 需要 `/dev/fuse` 与 `fusermount`/`fusermount3`；macOS 需要 macFUSE |
| 系统钥匙串 | 保存密码时必需 | Linux Secret Service（GNOME Keyring/KWallet 等）；macOS Keychain；Windows Credential Manager |

Linux 安装示例：

```bash
# Debian / Ubuntu
sudo apt install sshfs fuse3 openssh-client

# Arch Linux
sudo pacman -S sshfs fuse3 openssh

# Fedora
sudo dnf install fuse-sshfs fuse3 openssh-clients
```

macOS：

```bash
brew install --cask macfuse
brew install sshfs
```

macFUSE 安装后通常需要重启，并在“系统设置 → 隐私与安全性”中允许扩展。

> Windows 可编译配置管理相关代码，但 sshfs/FUSE 挂载未作为主要目标验证，推荐 Linux/macOS。

## 安装

```bash
git clone https://github.com/<your-name>/betterSSH.git
cd betterSSH

make build          # 生成 bin/betterSSH
make test           # go test ./...
make check          # gofmt + go vet + test + build

sudo make install   # 默认安装到 /usr/local/bin/betterSSH
# 或
go install ./cmd/betterSSH
```

也可以直接构建：

```bash
go build -o betterSSH ./cmd/betterSSH
./betterSSH help
```

## 快速开始

### 启动 TUI

```bash
betterSSH
# 或显式指定
betterSSH tui
```

TUI 启动后会读取 `~/.ssh/config`。按 `/` 搜索，`Enter` 执行默认动作，`m` 强制挂载并连接。

### 常用 CLI

```bash
betterSSH list
betterSSH show prod
betterSSH connect prod
betterSSH mount prod
betterSSH unmount prod --yes
```

## CLI 快速操作

不需要进入 TUI 也可以完成常用操作：

| 命令 | 作用 |
|---|---|
| `betterSSH list` / `ls` | 列出 Host |
| `betterSSH show <alias>` / `get` | 显示解析后的字段、挂载点、远程路径、挂载状态和 keyring key |
| `betterSSH connect <alias>` / `ssh` | 启动原生 SSH；默认动作由 settings 的 `default_action` 决定 |
| `betterSSH mount <alias>` | 仅执行 sshfs 挂载，成功后保留挂载并退出 |
| `betterSSH unmount <alias>` / `umount` | 卸载，需确认或 `--yes` |
| `betterSSH add <alias> [flags]` | 新增 Host |
| `betterSSH edit <alias> [flags]` | 修改常用字段，保留注释和未知字段；需确认或 `--yes` |
| `betterSSH remove <alias>` / `rm` / `delete` | 删除 Host 并自动删除对应钥匙串密码，需确认或 `--yes` |

### CLI 示例

```bash
# 查看 Host 的最终连接参数、挂载点和当前挂载状态
betterSSH show prod

# 按 settings 的默认动作连接
betterSSH connect prod

# 强制挂载后连接；SSH 退出时卸载本次新建的挂载
betterSSH connect prod --mount --unmount-after

# 仅连接，不挂载
betterSSH connect prod --no-mount

# 只挂载，立即返回
betterSSH mount prod
betterSSH mount prod --remote /srv/app --mountpoint ~/mnt/prod
betterSSH mount prod --remount

# 卸载（脚本中可加 --yes）
betterSSH unmount prod --yes

# 新增 / 修改 / 删除 Host；flags 可放在别名前后
betterSSH add beta --hostname beta.example.com --user bob --port 2200
betterSSH edit beta --identity-file ~/.ssh/id_beta --yes
betterSSH remove beta --yes
```

`add` / `edit` 支持的字段：

```text
--hostname  --user  --port  --identity-file  --proxy-jump  --server-alive
```

CLI 行为说明：

- `connect` 默认读取 `default_action`；`--mount` 强制挂载后连接，`--no-mount` 强制仅连接。
- `connect --mount` 遇到已有挂载点会复用并打印路径。
- `connect --mount` 支持 `--remote` / `--mountpoint` 临时覆盖。
- SSH 退出后，若 `auto_unmount=true` 且本次新建了挂载，会自动卸载；也可显式加 `--unmount-after`。否则保留挂载并打印卸载命令。
- `mount` 命令不会在退出时自动卸载；会打印完整挂载路径。
- `--mountpoint` / `--remote` 是本次命令的临时覆盖，不写入 settings；之后卸载自定义挂载点时要传同样的 `--mountpoint`。
- `edit` / `remove` / `unmount` 默认要求输入 `y` 确认；CI/脚本使用 `--yes`。
- `remove` 会在删除 Host 后自动删除对应系统钥匙串密码；删除失败会保留 Host 已删除的结果并报告 key 和错误。
- 密码不接受任何 CLI 参数。CLI 的 `connect` / `mount` 会复用系统钥匙串中的密码，并通过 AskPass 自动填充；保存密码仍通过 TUI 的 `p` 或编辑表单完成。
- CLI 操作同样遵守 `config.bak` 备份和外部修改检测。

## TUI 快捷键

| 键 | 动作 |
|---|---|
| `Enter` | 执行“默认动作”（默认仅连接，可在设置中改为挂载并连接） |
| `m` / `M` | **挂载并连接**（强制动作） |
| `e` | 编辑选中 Host（保存覆盖需二次确认） |
| `n` | 新建 Host |
| `d` | 删除 Host 及其钥匙串密码（二次确认） |
| `u` | 卸载选中 Host 的挂载点（二次确认） |
| `s` | 设置 |
| `p` | 保存密码到系统钥匙串（输入 + 写入确认） |
| `x` | 删除已保存密码（二次确认） |
| `/` | 模糊搜索；`Esc` 清除，`Enter` 保留过滤结果 |
| `r` | 从磁盘重新加载 config 与 settings |
| `?` | 帮助、依赖检查、安全说明 |
| SSH 退出后 | `Enter`/`k` 继续保留挂载，`u` 卸载并释放，`Esc` 保持现状 |
| `q` / `Ctrl+C` | 退出 |

很多终端无法区分 `Ctrl+M` 与 `Enter`（两者都是 CR），因此强制挂载请使用无歧义的 `m`。`Enter` 的行为可在设置里切换：

- `connect`：只启动 `ssh <alias>`。
- `mount-connect`：先 sshfs 挂载，再启动 `ssh <alias>`。

## 挂载与连接流程

1. 从解析后的 Host 读取 HostName、User、Port、IdentityFile、ProxyJump。
2. 计算挂载点（默认 `~/sshfs/<alias>`）并创建目录。
3. 主界面预先显示挂载点和远程路径；挂载/启动 ssh 前终端也会打印清晰目标。
4. 若已挂载：
   - TUI 按 `on_mounted` 设置询问 / 复用 / 先卸载；
   - CLI `mount` / `connect --mount` 默认复用并打印路径。
5. 执行类似命令：

   ```text
   sshfs alice@example.com:/ ~/sshfs/example \
     -p 22 -o IdentityFile=~/.ssh/id_ed25519 \
     -o reconnect -o ServerAliveInterval=15
   ```

6. 挂载成功后执行原生 `ssh <alias>`，将终端控制权完整交给 OpenSSH。
7. SSH 会话退出后：
   - TUI 默认弹窗询问继续保留还是卸载；
   - CLI 默认保留并打印卸载命令，`--unmount-after` 或 `auto_unmount=true` 自动卸载。
8. 挂载失败时显示原因和提示。默认允许继续 SSH；严格模式下中止。

挂载点安全检查会拒绝 `/`、用户 home 目录以及 home 的父目录，避免把重要数据遮蔽。

## 设置

路径：`~/.config/betterSSH/settings.json`（遵循 `XDG_CONFIG_HOME`），权限 `0600`。

```json
{
  "version": 1,
  "mount_root": "~/sshfs",
  "remote_path": "/",
  "default_action": "connect",
  "on_mounted": "ask",
  "auto_unmount": false,
  "strict_mount": false,
  "mount_points": {
    "prod": "~/mnt/prod"
  },
  "remote_paths": {
    "prod": "/srv/app"
  }
}
```

- `mount_root` 支持 `~` 和 `{alias}` 占位符，例如 `/mnt/{alias}`。
- `mount_points` / `remote_paths` 为按 Host 别名覆盖；目前通过编辑 JSON 配置。
- `default_action`：`connect` 或 `mount-connect`。
- `on_mounted`：`ask` / `unmount` / `use`。
- `auto_unmount`：SSH 会话结束后跳过询问、自动卸载。默认关闭。
- `strict_mount`：挂载失败后禁止继续连接。默认关闭。

## 密码与 AskPass

### 保存位置

- service：`betterSSH`
- key：`<user>@<hostname>:<port>`，例如 `alice@example.com:22`
- 实现：`github.com/zalando/go-keyring`
- 不保存明文密码到 config、SQLite、JSON、日志、argv 或环境变量。

保存入口：

- 编辑 Host 表单，按 `Ctrl+S` 启用“保存密码”，输入密码后保存；
- 主界面按 `p` 直接为选中 Host 保存密码；
- 主界面按 `x` 删除已保存密码。

> 工具不会截获首次原生 `ssh` 会话中的键盘输入；首次连接前请使用上述入口保存密码。

### AskPass 机制

连接时如果钥匙串中存在密码，且 `ssh -V` 表明 OpenSSH >= 8.4，则：

```text
SSH_ASKPASS=<当前 betterSSH 可执行文件绝对路径>
SSH_ASKPASS_REQUIRE=force
SSH_ENHANCED_ASKPASS=1
SSH_ENHANCED_ASKPASS_KEY=alice@example.com:22
```

环境变量里只有 keyring key，没有密码。OpenSSH 需要密码时会调用：

```bash
betterSSH askpass "alice@example.com's password:"
```

隐藏的 AskPass 模式从系统钥匙串读取密码并输出到 stdout，忽略 OpenSSH 传入的提示文本；错误信息只输出通用失败原因，不包含密码。

如果 OpenSSH 低于 8.4、AskPass 不可用或钥匙串读取失败，程序会去掉 AskPass 环境变量，回退到终端交互输入，并在界面/CLI 中提示。

## 安全设计

- 不调用 `sshpass`。
- 密码不会出现在命令行参数、环境变量、配置文件、日志或错误信息中。
- 密码只写入操作系统钥匙串；AskPass 将密码写 stdout，这是 OpenSSH 协议要求。
- 已有 config 时写回前备份为 `config.bak`（权限 `0600`）。
- 写回使用临时文件 + `rename` 原子替换；写失败不会破坏原文件。
- 保存前比较磁盘内容，若被其他程序修改则返回 `ErrModified`，拒绝静默覆盖。
- 删除 Host（同时删除对应钥匙串密码）、覆盖 Host、卸载、删除密码、保存设置均需二次确认。
- 挂载点限制，拒绝 `/`、home 及 home 父目录。
- SSH 和 sshfs 通过 `os/exec` 直接以 argv 调用，不经过 shell，避免注入。
- 子进程退出码、`Ctrl+C`、终端尺寸变化都有处理。
- `.gitignore` 默认忽略私钥、证书、`.env`、构建产物和本地缓存，降低误提交风险。

> 注意：`.gitignore` 只能降低误提交概率，不能替代提交前检查。公开仓库前请运行 `git status` 并确认没有敏感信息。

## 架构

```text
cmd/betterSSH/
  main.go              入口、命令分发、AskPass
  cli.go               CLI 子命令
internal/config        块级解析、有效值解析、非破坏性写回、备份
internal/keyring       钥匙串封装与 key 生成
internal/settings      工具设置读写与校验
internal/ssh           ssh/sshfs 命令构造、AskPass、版本探测
internal/mount         挂载点安全、挂载状态、Linux/macOS 卸载
internal/tui           Bubble Tea 模型、表单、视图、消息流
internal/version       版本注入
```

外部命令通过 `mount.Runner` 等接口注入；`internal/ssh` 的命令构造和 AskPass 逻辑可独立测试。

## 开发与测试

```bash
make fmt       # gofmt
make vet       # go vet ./...
make test      # go test ./...
make build     # 构建 bin/betterSSH
make check     # 全量检查
```

测试覆盖：

- config 解析、注释/未知字段保留、CRLF、备份、外部修改拒绝、通配默认值解析。
- keyring key 生成。
- ssh/sshfs 参数、IPv6、AskPass 环境、AskPass 模式、版本解析。
- settings 默认值、持久化、挂载点计算。
- mount 挂载表解析、安全校验、Linux 卸载回退。
- TUI 列表过滤、删除确认、编辑覆盖确认、密码保存、搜索、SSH 退出后的挂载保留/卸载分支。
- CLI 参数解析、add/edit/remove、show、未挂载状态、`--yes` 确认和各子命令退出码。

## 已知限制

- `Include` 引入的 Host 不会展开列出或编辑；`Match` 块会被原样保留但不在 TUI 中展示。
- 通配 Host（如 `Host *`、`Host *.example`）会被保留，但不会作为连接目标列出。
- TUI 只管理常用字段；其他字段原样保留。重复的受管字段只更新第一条，清空字段会删除该字段的所有重复行。
- `config.bak` 每次写回覆盖为最近一次写前备份；不保存历史版本。
- 配置为符号链接时，写入符号链接目标；备份生成在链接路径旁（`<链接路径>.bak`）。
- 密码 key 基于 User + HostName + Port。修改这三项后旧钥匙串记录不会自动迁移，需要按 `x` 删除旧的、按 `p` 重新保存。
- 删除 Host 会自动删除当前解析出的 `User + HostName + Port` 对应密码；若之前修改过这三项，旧的钥匙串记录不会自动迁移或删除。
- 自动填充密码在 OpenSSH < 8.4 上不可用，会回退到终端输入。
- sshfs 挂载依赖远程 SFTP 子系统与权限；挂载 `/` 成功不代表能读取远程所有内容。权限不足时使用 `~` 或 `remote_paths` 指定目录。
- Linux 无 Secret Service 的纯 headless 环境可能没有可用钥匙串；此时保存密码会失败，但普通 SSH 连接仍可用。
- Windows 可编译配置管理 TUI，但 sshfs/FUSE 挂载与覆盖写回未作为主要目标验证；推荐 Linux/macOS。
- 终端通常无法区分 `Ctrl+M` 与 `Enter`，强制挂载请使用 `m`。

## 贡献

欢迎提交 Issue 和 Pull Request。提交前请确保：

1. `make check` 通过。
2. 不提交任何真实主机、IP、用户名、SSH 私钥、密码、证书或日志。
3. 截图和示例数据使用 `example.com`、`alice` 等虚构值。

## 使用声明

本项目仅用于合法的 SSH 连接与主机配置管理。请遵守目标服务器的访问政策；作者不对滥用承担责任。

本项目未附带特定开源许可证。如需公开分发，请根据你的发布意图补充 `LICENSE` 文件。

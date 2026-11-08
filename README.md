# rbackup-ui

rbackup-cli 的终端用户界面（TUI），用于管理和执行多任务 rsync 远程备份。

- 语言: Go
- TUI 框架: tview + tcell
- 后端: rbackup-cli
- 平台: Linux、Windows (MSYS2)

---

## 目录

- [功能](#功能)
- [界面说明](#界面说明)
  - [布局尺寸](#布局尺寸)
  - [焦点区（3 个）](#焦点区3-个)
  - [状态栏（3 行）](#状态栏3-行)
  - [顶部信息](#顶部信息)
- [安装](#安装)
  - [方式一：懒人模式（不需要源码，不需要 Go）](#方式一懒人模式不需要源码不需要-go)
  - [方式二：git clone 模式（不需要 Go）](#方式二git-clone-模式不需要-go)
  - [方式三：手动模式（源码 + Go，自己编译）](#方式三手动模式源码--go自己编译)
  - [卸载](#卸载)
  - [手动解压（不想用安装器）](#手动解压不想用安装器)
- [配置](#配置)
  - [全局配置](#全局配置)
  - [任务配置](#任务配置)
  - [关于 --chown](#关于---chown)
  - [关于时间戳](#关于时间戳)
  - [示例配置一：明文源 → 明文挂载点](#示例配置一明文源--明文挂载点)
  - [示例配置二：密文源 → 密文目录](#示例配置二密文源--密文目录)
  - [两份示例的对比](#两份示例的对比)
  - [使用示例配置](#使用示例配置)
- [使用](#使用)
  - [启动](#启动)
  - [命令行参数](#命令行参数)
  - [脚本路径查找顺序](#脚本路径查找顺序)
  - [典型流程](#典型流程)
- [Windows（原生：Git Bash / MSYS2）](#windows原生git-bash--msys2)
- [Web UI（可选）](#web-ui可选)
  - [启动](#启动-1)
  - [参数](#参数)
  - [登录方式（token / 用户名密码 / TOTP 二次验证）](#登录方式token--用户名密码--totp-二次验证)
  - [安全中心（2FA 自助管理）](#安全中心2fa-自助管理)
  - [手机扫码登录](#手机扫码登录)
  - [界面主题与中英文](#界面主题与中英文)
  - [界面主题与手机](#界面主题与手机)
  - [安全默认（fail-closed）](#安全默认fail-closed)
  - [远程/手机访问](#远程手机访问)
  - [页面功能](#页面功能)
  - [限制](#限制)
- [快捷键](#快捷键)
  - [全局](#全局)
  - [焦点 1：信息区（header）](#焦点-1信息区header)
  - [焦点 2：任务区（table + 命令区）](#焦点-2任务区table--命令区)
  - [焦点 3：交互区（interact）](#焦点-3交互区interact)
  - [空闲状态（无运行、无确认）](#空闲状态无运行无确认)
  - [运行中（任何焦点）](#运行中任何焦点)
  - [危险确认中（交互区焦点）](#危险确认中交互区焦点)
- [挂载门禁](#挂载门禁)
  - [语义](#语义)
  - [require_mounted](#require_mounted)
  - [require_unmounted](#require_unmounted)
  - [两者互斥](#两者互斥)
  - [软链接挂载点](#软链接挂载点)
- [统计与日志](#统计与日志)
  - [文件位置](#文件位置)
  - [统计文件格式](#统计文件格式)
  - [用命令行读取统计](#用命令行读取统计)
  - [日志与 TUI 的关系](#日志与-tui-的关系)
  - [运行时长与数据](#运行时长与数据)
  - [分隔线](#分隔线)
  - [退出码](#退出码)
- [常见问题排查](#常见问题排查)
  - [挂载检查误报"未挂载"](#挂载检查误报未挂载)
  - [rsync 报"failed to set times"](#rsync-报failed-to-set-times)
  - [rsync 报"You can only specify a user-affecting --chown once"](#rsync-报you-can-only-specify-a-user-affecting---chown-once)
  - [rsync 报"Permission denied (13)"](#rsync-报permission-denied-13)
  - [日志里中文显示为 \#345\#267\#245](#日志里中文显示为-345267245)
- [构建](#构建)
  - [本地平台](#本地平台)
  - [交叉编译](#交叉编译)
  - [产物位置](#产物位置)
  - [版本注入](#版本注入)
- [打包与发布](#打包与发布)
  - [打包](#打包)
  - [发布到 GitHub Release](#发布到-github-release)
  - [本地打包（不上传）](#本地打包不上传)
  - [其他发布命令](#其他发布命令)
  - [参数](#参数-1)
- [环境变量](#环境变量)
- [常见问题](#常见问题)
  - [F1 无反应？](#f1-无反应)
  - [按 F1 或 ? 后帮助浮层出现但按键无响应？](#按-f1-或--后帮助浮层出现但按键无响应)
  - [Windows 下按键重复触发？](#windows-下按键重复触发)
  - [Windows 下 ESC 无法退出？](#windows-下-esc-无法退出)
  - [Linux 下 rbackup-tui 找不到 bash？](#linux-下-rbackup-tui-找不到-bash)
  - [rsync 输出没显示？](#rsync-输出没显示)
  - [日志目录不可写？](#日志目录不可写)
  - [如何只做挂载检查？](#如何只做挂载检查)
  - [危险确认时怎么快速跳过全部？](#危险确认时怎么快速跳过全部)
  - [如何强制退出？](#如何强制退出)
  - [顶部日志和统计路径显示为相对路径？](#顶部日志和统计路径显示为相对路径)
  - [统计文件去哪了？](#统计文件去哪了)
  - [状态栏按键提示方括号 `[a]` 显示不出来？](#状态栏按键提示方括号-a-显示不出来)
  - [按 Enter / d 后 TUI 卡死？](#按-enter--d-后-tui-卡死)
  - [命令太长显示不全？](#命令太长显示不全)
  - [日志行太长看不全？](#日志行太长看不全)
  - [想看终端首行/末行？](#想看终端首行末行)
- [文件结构](#文件结构)
- [作者](#作者)
- [许可证](#许可证)

---

## 功能

- 多任务管理：读取 config.ini，列出全部任务
- 批量选择：空格 / a / n 选择任务
- 运行 / 预览：Enter 运行，d 预演（dry-run）
- 危险操作二次确认：--delete、--remove-source-files 逐个确认，支持 y/n/a/s
- 挂载门禁：支持 require_mounted / require_unmounted
- 软链接挂载点兼容：用 realpath 解析路径后再比对
- 实时日志：rsync 输出实时滚动，可暂停、翻页
- 累积统计：成功 / 跳过 / 失败 / 挂载门禁失败
- 时长统计：每个任务用时 + 总用时（分钟/秒）
- 数据统计：文件数、总大小、发送/接收字节、平均速率
- 独立统计文件：与日志同目录，同名不同后缀（.stats）
- 错误摘要：失败任务自动提取错误行
- 等效命令：命令区实时显示将要执行的 rsync 命令（支持横滚）
- 三焦点区：信息区 / 任务区 / 交互区，Tab 循环切换
- 三行状态栏：状态 / 当前焦点按键 / 全局按键
- 帮助浮层：F1 或 ?
- 版本信息：编译时通过 -ldflags 注入 git 版本

---

## 界面说明

![rbackup-ui 界面示意](docs/images/ui-overview.svg)

上图由 `tools/gen-ui-preview.py` 生成（矢量图，可随界面调整重新生成）。
终端高度 ≥ 31 行时完整显示；下面折叠块是同一布局的等宽纯文本版。

<details>
<summary>纯文本版（终端 / 离线阅读）</summary>

```text
┌─ rbackup ─────────────────────────────────────────────────────────────────────────────┐
│脚本: ~/.local/bin/rbackup-cli   配置: ~/.config/rbackup-ui/config.ini                  │
│远端: admin@example.com:22   策略: skip（门禁失败时跳过）                               │
│日志: ~/.local/state/rbackup-ui/log/20260928_1922.log                                  │
│统计: ~/.local/state/rbackup-ui/log/20260928_1922.stats                                │
├─ [2] 任务列表 ────────────────────────────────────────────────────────────────────────┤
│    任务名          源                                目标                  门禁        │
│  ● alice           /d/alice/my_company/              /srv/.../doc-alice    已挂载      │
│  ● bob             /d/bob/my_company/                /srv/.../doc-bob      已挂载      │
│  ○ charlie         /d/charlie/DevOps                 /srv/st1000dm         —           │
│  ○ Test1           ~/rsync/test1.d/                  /srv/st1000dm/t1      已挂载      │
│  ○ Test2           ~/rsync/test2.d/                  /srv/st1000dm/t2      未挂载      │
│                                                                                        │
│> 命令: rsync -avzhu --progress --delete --exclude='/.deleted_files/'                   │
│  --backup --backup-dir="/srv/.../rbackup/<ts>" -e "ssh -p 22 -i ..." ...               │
├─ [3] 交互区 ──────────────────────────────────────────────────────────────────────────┤
│[2026-09-28 19:24:10] 开始实际执行：共 2 个任务                                         │
│>>> [1/2] alice 成功    用时: 47s    累积: 成功 1  跳过 0  失败 0  挂载门禁失败 0       │
│     传输: 文件: 8/463  总大小: 26.00M  数据: 发送 28.97K + 接收 330B  速率: 661.60 KB/s│
│...                                                                                     │
├───────────────────────────────────────────────────────────────────────────────────────┤
│运行中   进度 2/5   当前 bob   成功 1 跳过 0 失败 0 挂载门禁失败 0   用时 1min32s       │
│滚动: 移动[↑↓/jk] 横滚[←→/hl] 翻页[PgUp/PgDn]  选择: 勾选[空格] 全选[a] 清空[n]         │
│全局: 切换[Tab] 直选[1/2/3] 停止[Ctrl+C] 退出[q] 帮助[F1/?]                             │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

</details>

### 布局尺寸

    header              4 行 (边框 2 + 内容 2)
    tableArea           权重 3 (边框 2 + 表格 + 命令区 2)
    interact            权重 2 (边框 2 + 日志)
    状态栏              3 行
    合计                约 31 行

### 焦点区（3 个）

| 编号 | 区域 | 边框色（焦点时） | 说明 |
|---|---|---|---|
| 1 | 信息区 (header) | 绿色 | 顶部两行信息，支持横滚 |
| 2 | 任务区 (table) | 绿色 | 任务表格 + 命令区 |
| 3 | 交互区 (interact) | 绿色 | 实时日志，支持横滚 |

切换方式：`Tab` 循环 1→2→3→1；`1` / `2` / `3` 直选。

### 状态栏（3 行）

- **第 1 行**：状态 + 进度 + 累积统计（危险确认时红/黄闪烁）
- **第 2 行**：当前焦点区的按键提示（分类显示）
- **第 3 行**：全局按键（固定显示）

### 顶部信息

- 第一行：脚本路径、配置文件、挂载策略（含中文描述）
- 第二行：远端主机、日志文件、统计文件

所有路径自动将 `$HOME` 前缀缩写为 `~`。

---

## 安装

三种方式，按你手上有什么选一种。

### 方式一：懒人模式（不需要源码，不需要 Go）

一条命令，自动识别系统与架构，从 GitHub Releases 下载预编译包并校验 SHA256：

    # 用户级（无需 sudo）
    curl -fsSL https://github.com/lockejet/rbackup-ui/releases/latest/download/install.sh | bash

    # 系统级（装到 /usr/local/bin，需要 sudo）
    curl -fsSL https://github.com/lockejet/rbackup-ui/releases/latest/download/install.sh | sudo bash -s -- --system

常用参数：

| 参数 | 说明 |
|---|---|
| `--system` | 系统级安装 |
| `--prefix DIR` | 自定义安装前缀 |
| `--version vX.Y.Z` | 指定版本（默认 latest） |
| `--from FILE` | 用本地预编译包安装（离线） |
| `--uninstall` | 卸载（保留配置与日志） |
| `--dry-run` | 只打印将要做什么 |
| `--require-checksum` | 缺少 SHA256SUMS 时直接失败 |

安装布局：

| | 用户级 | 系统级 |
|---|---|---|
| TUI 程序 | `~/.local/bin/rbackup-tui` | `/usr/local/bin/rbackup-tui` |
| Web 程序 | `~/.local/bin/rbackup-web`（与 TUI 同一文件，硬链接） | `/usr/local/bin/rbackup-web` |
| 后端脚本 | `~/.local/bin/rbackup-cli` | `/usr/local/bin/rbackup-cli` |
| 配置 | `~/.config/rbackup-ui/config.ini` | 仍按调用 sudo 的那个用户：`~/.config/rbackup-ui/config.ini` |
| 日志 | `LOG_DIR` 可覆盖；默认 `$XDG_STATE_HOME/rbackup-ui/log`（Windows `%LOCALAPPDATA%\rbackup-ui\Logs`） | 同左 |
| 样例 / 文档 | `~/.local/share/rbackup-ui/` | `/usr/local/share/rbackup-ui/` |

各路径遵循 XDG 规范：`XDG_CONFIG_HOME`（Windows 为 `%AppData%`）、
`XDG_STATE_HOME`、`XDG_DATA_HOME` 设置后按设置生效。日志在 Windows 上用
`%LOCALAPPDATA%\rbackup-ui\Logs`（无 `XDG_STATE_HOME` 概念）。

Windows（MSYS2 / Git Bash）下用户级安装到 `~/.local/bin`；`--system` 装到
`%LOCALAPPDATA%/Programs/rbackup-ui`（无需管理员权限）。

### 方式二：git clone 模式（不需要 Go）

    git clone https://github.com/lockejet/rbackup-ui.git
    cd rbackup-ui

    make install-prebuilt                 # 用户级，装当前检出对应的版本
    make install-prebuilt SYSTEM=1        # 系统级
    make install-prebuilt VERSION=vX.Y.Z  # 指定版本
    make install-prebuilt FROM=dist/rbackup-ui-vX.Y.Z-linux-amd64.tar.gz   # 离线

全程只下载预编译包，不会调用 `go`。

### 方式三：手动模式（源码 + Go，自己编译）

依赖：Go 1.27+（见 `go.mod`）

    git clone https://github.com/lockejet/rbackup-ui.git
    cd rbackup-ui

    make install            # 用户级：编译并装到 ~/.local/bin
    make install-system     # 系统级：编译后 sudo 装到 /usr/local/bin
    make build              # 只编译不安装
    make package            # 打三平台发布包

### 卸载

    make uninstall                                          # 在克隆目录里执行
    curl -fsSL .../install.sh | bash -s -- --uninstall       # 预编译安装的

只删除程序文件（含安装清单里记录的所有文件），保留 `~/.config/rbackup-ui/` 下的配置与日志。

### 手动解压（不想用安装器）

从 GitHub Releases 下载对应平台的压缩包，解压后目录结构：

    rbackup-ui-linux-amd64/
    ├── rbackup-tui
    ├── rbackup-web        # 与 rbackup-tui 同一 inode（硬链接）
    ├── rbackup-cli
    ├── config1.ini.example
    ├── config2.ini.example
    ├── README.md
    └── LICENSE

复制示例配置后即可运行：

    cp config2.ini.example config2.ini
    vim config2.ini
    ./rbackup-tui -c config2.ini -s rbackup-cli

`rbackup-cli` 与二进制同目录时，`-s` 可以省略。

---

## 配置

rbackup-tui / rbackup-web 与 rbackup-cli 共用 config.ini，格式一致。

### 全局配置

| 键 | 说明 | 默认 |
|---|---|---|
| HOST | 远端主机 | localhost |
| SSH_PORT | SSH 端口 | 22 |
| SSH_USER | SSH 用户名 | admin |
| SSH_KEY | SSH 私钥路径 | ~/.ssh/id_ed25519 |
| LOG_DIR | 日志目录（可选覆盖） | `$XDG_STATE_HOME/rbackup-ui/log`；Windows `%LOCALAPPDATA%\rbackup-ui\Logs` |
| GLOBAL_OPTS | rsync 全局选项 | -avzhu --progress |
| RSYNC_PATH | 提权命令 | sudo rsync |
| DEFAULT_REMOVE_SOURCE | 源端删除默认值 | no |
| MOUNT_POLICY | 门禁失败策略 | skip |

MOUNT_POLICY 取值：

- skip：跳过，不算失败
- fail：记失败，影响退出码
- ignore：不检查

### 任务配置

    [task_example]
    src=/path/to/source/
    dst=/path/to/target/
    opts=--no-perms --chown=user:group --update
    delete=yes
    remove_source=no
    require_mounted=yes
    mount_point=/path/to/mount/point
    mount_fstype=fuse.gocryptfs

| 键 | 说明 |
|---|---|
| src | 本地源路径（~ 会展开） |
| dst | 远端目标路径 |
| opts | 任务级 rsync 附加选项 |
| delete | yes / no，启用回收站模式 |
| remove_source | yes / no，同步后删除源文件 |
| require_mounted | yes，要求 mount_point 已挂载 |
| require_unmounted | yes，要求 mount_point 未挂载 |
| mount_point | 要检查的挂载点（省略时默认取 dst） |
| mount_fstype | require_mounted 时的期望类型 |

兼容旧键：

- require_mount=yes 等价于 require_mounted=yes
- require_mount=no 等价于不检查
- mount_path 等价于 mount_point

推荐使用新键，但旧键依然有效。

### 关于 --chown

rsync 只允许 `--chown` 出现一次。可以放在：

- `GLOBAL_OPTS` 里（所有任务共用）
- 每个任务的 `opts` 里（任务各自控制）

不能两处都写，否则报错：

    rsync: You can only specify a user-affecting --chown once.

推荐放 `GLOBAL_OPTS`，任务 opts 里不再重复。

### 关于时间戳

加密文件系统（gocryptfs 等）挂载点常出现：

    rsync: [generator] failed to set times on "...": Operation not permitted (1)
    rsync error: some files/attrs were not transferred (see previous errors) (code 23)

原因是 rsync 尝试把源端 mtime 设置到目标，但被挂载点拒绝。

解决：在 `GLOBAL_OPTS` 里加 `--omit-dir-times`（跳过目录时间戳），如需跳过文件时间戳再加 `--no-times`。

| 选项 | 作用 |
|---|---|
| `--no-times` | 不设置文件的 mtime |
| `--omit-dir-times` | 不设置目录的 mtime |

推荐组合：

    GLOBAL_OPTS=-avzhu --progress --no-perms --omit-dir-times --chown=admin:users --update --delete-after

如果仍有零星 code 23 报错，不影响内容同步，可忽略。

### 示例配置一：明文源 → 明文挂载点

对应 `config1.ini.example`。源是明文目录，目标是已解密的 gocryptfs 挂载点，使用旧键 `require_mount` / `mount_path` 书写。

    # ============================================================
    # 全局配置
    # ============================================================
    HOST=example.com
    SSH_PORT=22
    SSH_KEY=~/.ssh/id_ed25519
    # 日志目录（可选；不写则跟随平台：
    #   Linux $XDG_STATE_HOME/rbackup-ui/log，Windows %LOCALAPPDATA%\rbackup-ui\Logs）
    #LOG_DIR=/var/log/rbackup
    GLOBAL_OPTS=-avzhu --progress --no-perms --omit-dir-times --chown=admin:users --update --delete-after
    RSYNC_PATH=sudo rsync
    DEFAULT_REMOVE_SOURCE=no

    # 挂载门禁策略: skip | fail | ignore
    MOUNT_POLICY=skip

    # ============================================================
    # 任务
    # ============================================================
    [task_alice]
    src=/d/alice/my_company/
    dst=/srv/st1000dm/Work/doc-alice
    opts=
    delete=yes
    remove_source=no
    require_mount=yes
    mount_path=/srv/st1000dm/Work
    mount_fstype=fuse.gocryptfs

    [task_bob]
    src=/d/bob/my_company/
    dst=/srv/st1000dm/Work/doc-bob
    opts=
    delete=yes
    remove_source=no
    require_mount=yes
    mount_path=/srv/st1000dm/Work
    mount_fstype=fuse.gocryptfs

    [task_charlie]
    src=/d/charlie/DevOps
    dst=/srv/st1000dm
    opts=
    delete=yes
    remove_source=no
    require_mount=no

    [task_Test1]
    src=~/rsync/test1.d/
    dst=/srv/st1000dm/test1.d
    delete=yes
    require_mount=yes
    mount_path=/srv/st1000dm/test1.d
    mount_fstype=fuse.gocryptfs

    [task_Test2]
    src=~/rsync/test2.d/
    dst=/srv/st1000dm/test2.d
    delete=yes
    require_mount=no
    mount_path=/srv/st1000dm/test2.d
    mount_fstype=fuse.gocryptfs

**特点**：

- 使用旧键 `require_mount` / `mount_path`（兼容写法）
- 门禁模式：`require_mounted`（目标必须是已挂载的明文挂载点）
- 源是明文，目标是已解密的 gocryptfs 挂载点

### 示例配置二：密文源 → 密文目录

对应 `config2.ini.example`。源是 gocryptfs 底层密文目录，目标是另一个 gocryptfs 底层密文目录，使用新键 `require_unmounted` / `mount_point` 书写。

    # ============================================================
    # 备份配置文件示例
    # 放置于脚本同目录下，或通过 --config 指定其他路径
    # ============================================================

    # 全局 SSH 参数
    HOST=example.com
    SSH_PORT=22
    SSH_KEY=~/.ssh/id_ed25519

    # 日志目录（可选；不写则跟随平台：
    #   Linux $XDG_STATE_HOME/rbackup-ui/log，Windows %LOCALAPPDATA%\rbackup-ui\Logs）
    #LOG_DIR=/var/log/rbackup

    # 全局 rsync 默认选项（可被任务级 opts 追加覆盖）
    GLOBAL_OPTS=-avzhu --progress --omit-dir-times

    # 提权选项，需要命令行中用 --sudo 激活
    RSYNC_PATH=sudo rsync

    # ============================================================
    # 任务定义
    # ============================================================

    [task_Personal_Vault]
    src=/srv/st1000dm/Personal_Vault
    dst=/mnt/wd4000g
    opts=--size-only
    delete=yes

    [task_Study]
    src=/srv/st1000dm/Study
    dst=/mnt/wd4000g
    delete=yes

    [task_Life]
    src=/srv/st1000dm/Life
    dst=/mnt/wd4000g
    opts=-rtvh
    delete=yes

    [task_infra_secrets_cipher]
    src=/srv/st1000dm/.cipher.d/infra_secrets
    dst=/mnt/wd4000g/.cipher.d
    opts=-avh
    delete=yes
    require_unmounted=yes
    mount_point=/mnt/wd4000g/infra_secrets
    mount_fstype=fuse.gocryptfs

    [task_Work_Cipher]
    src=/srv/st1000dm/.cipher.d/Work
    dst=/mnt/wd4000g/.cipher.d
    opts=-avh
    delete=yes
    require_unmounted=yes
    mount_point=/mnt/wd4000g/Work
    mount_fstype=fuse.gocryptfs

**特点**：

- 使用新键 `require_unmounted` / `mount_point`
- 门禁模式：`require_unmounted`（目标明文挂载点必须未挂载）
- 源是密文，目标是 gocryptfs 底层密文目录
- 前三个任务（Personal_Vault、Study、Life）未加密，不设门禁

### 两份示例的对比

| 项 | 示例一（config1） | 示例二（config2） |
|---|---|---|
| 源 | 明文目录 | 密文目录 |
| 目标 | 明文挂载点 | 密文目录 |
| 门禁 | require_mount=yes（旧键） | require_unmounted=yes（新键） |
| 键风格 | 兼容旧键 | 推荐新键 |
| 典型任务 | task_alice、task_bob、task_charlie | task_Work_Cipher、task_infra_secrets_cipher |
| 门禁判定 | 目标必须已挂载 | 目标必须未挂载 |

### 使用示例配置

    cp config1.ini.example config1.ini
    vim config1.ini

    rbackup-tui -c config1.ini -s rbackup-cli

---

## 使用

### 启动

    # 本机使用（默认只监听 127.0.0.1:30031）
    rbackup-web

    # 指定 token 文件（避免 token 出现在 ps 中）
    rbackup-web --web-token-file /etc/rbackup-ui/web.token

    # 常驻服务（推荐：绑 loopback，由 Tailscale/WireGuard 或反代对外）
    openssl rand -base64 32 > /etc/rbackup-ui/web.token && chmod 600 /etc/rbackup-ui/web.token
    rbackup-web --web-token-file /etc/rbackup-ui/web.token

### 参数

| 参数 | 说明 |
|---|---|
| `--web` | 兼容写法：强制 Web 模式（用 `rbackup-web` 入口启动时无需该参数） |
| `--web-addr` | 监听地址，默认 `127.0.0.1:30031` |
| `--web-token-file` | token 文件（推荐，避免出现在 `ps`） |
| `--web-token` | 直接指定 token（不推荐） |
| `--web-tls-cert` / `--web-tls-key` | 直接启用 TLS（两者必须同时给） |
| `--web-insecure-http` | 允许"非 loopback + 明文 HTTP"（仅限隔离内网） |
| `--web-audit-log` | 审计日志文件，默认 `<日志目录>/web-audit.log`；`-` 只写 stderr |
| `--web-max-clients` | SSE 客户端上限，默认 8 |
| `--web-read-only` | 只读模式：只观察，禁止触发任务 |
| `--web-trust-proxy` | 信任反向代理头（`X-Forwarded-Proto/Host/For`），反代后必开 |
| `--web-public-url` | 对外基址（如 `https://backup.example.com`）；不设则按请求头自适应 |
| `--web-user` | 用户名（配合密码登录，默认 `admin`） |
| `--web-password-hash-file` | 密码哈希文件（用 `--web-hash-password` 生成） |
| `--web-2fa` | `off`（默认）/ `totp`：2FA 策略；认证器在 Web「安全」页扫码绑定（secret 文件可省略） |
| `--web-totp-secret-file` | TOTP secret 文件（可省略；Web 安全页绑定后自动生成） |

环境变量：`RBACKUP_WEB_TOKEN` / `RBACKUP_WEB_TOKEN_FILE` / `RBACKUP_WEB_ADDR` /
`RBACKUP_WEB_TRUST_PROXY`（1/true/yes/on）/ `RBACKUP_WEB_PUBLIC_URL`（命令行优先）。

### 登录方式（token / 用户名密码 / TOTP 二次验证）

三种方式可组合，token 始终保留为"忘记认证器"时的恢复入口。

```bash
# ① 生成密码哈希（交互输入密码，不回显）
rbackup-web --web-hash-password > ~/.config/rbackup-ui/web.passwd
chmod 600 ~/.config/rbackup-ui/web.passwd

# ② 生成 TOTP secret；终端里会直接显示二维码，手机 Authenticator 扫码即可
#    注意：这会生成新 secret；已录入过认证器就不要重跑，改用 --web-show-totp-qr
rbackup-web --web-gen-totp

# 如需把 secret 存文件（二维码在 stderr，不会污染 stdout）：
rbackup-web --web-gen-totp 2>/dev/null | sed -n '2p' > ~/.config/rbackup-ui/web.totp
chmod 600 ~/.config/rbackup-ui/web.totp

# 已经录入过认证器、只是想再显示一次二维码（不会轮换 secret）：
rbackup-web --web-show-totp-qr

# ③ 启动：默认监听 127.0.0.1:30031，密码 + TOTP，token 作为恢复方式
rbackup-web \
  --web-password-hash-file ~/.config/rbackup-ui/web.passwd \
  --web-2fa totp --web-totp-secret-file ~/.config/rbackup-ui/web.totp \
  --web-token-file ~/.config/rbackup-ui/web.token
```

- 密码哈希用 PBKDF2-HMAC-SHA256（60 万次迭代 + 随机盐），不存明文；
- TOTP 为 RFC 6238（SHA1/6 位/30 秒，允许 ±1 窗口），已用过的动态码不能复用；
- 登录失败限速：同 IP 每分钟 10 次；2FA 待验证会话 5 分钟过期、最多 5 次尝试；
- 密码登录在**非 loopback** 下仍要求 TLS（或 `--web-insecure-http` + 可信 VPN）。

### 安全中心（2FA 自助管理）

启动时加 `--web-2fa totp` 即开启 2FA 策略，**初始无需任何 secret 文件**：

1. 用密码登录后，若尚未绑定认证器，页面顶部会提示「当前未启用两步验证」，点「立即启用」；
2. 用手机 Authenticator 扫描二维码（或手动输入 secret），输入 6 位动态码完成绑定；
3. 绑定成功后一次性展示 10 个备份码（请立即保存）；
4. 之后每次密码登录都需要动态码或备份码；
5. 「安全」页还可**重新生成备份码**、**关闭 2FA**（均需当前动态码/备份码）。

token 登录不要求 2FA，始终作为忘记认证器时的恢复方式。

### 手机扫码登录

登录后点顶部「手机登录」：服务端生成一个 **120 秒有效、一次性** 的登录链接并显示二维码；
手机（同一局域网）扫码或打开链接即可直接建立会话，无需在手机上输入密码/动态码。
链接绑定当前访问地址；若服务只监听 loopback，会自动替换成局域网 IP。

### 界面主题与中英文

- 顶栏「🌓」切换 **自动 / 亮色 / 暗色**；运行日志背景跟随主题（与任务列表同底色）。
- 顶栏「中/EN」切换 **中文 / English**，选择保存在浏览器本地，默认跟随浏览器语言。

### 界面主题与手机

- 顶栏「🌓」按钮切换 **自动 / 亮色 / 暗色** 三种主题，选择保存在浏览器本地；
- 手机窄屏：任务列表自动切换为**卡片**、操作按钮固定在底部（含安全区适配）、
  触控目标 ≥44px；日志区独立滚动；登录输入框防 iOS 聚焦缩放。

### 安全默认（fail-closed）

- 默认只监听 `127.0.0.1`；
- 监听非 loopback 地址时**必须** token + TLS，否则**拒绝启动**；
- 会话用 HttpOnly + `SameSite=Strict` Cookie，写操作要求 CSRF 头；token 不进 URL；
- 危险任务（`delete=yes` / `remove_source=yes`）必须**先成功 Dry-run 预览**，再二次确认；
- 触发/取消/登录都写审计日志（`web-audit.log`）。

### 远程/手机访问

**方式一：SSH 隧道（最简单，零暴露）**

本机保持默认监听 `127.0.0.1:30031`，在你的电脑上建隧道，然后把浏览器指向本机：

    # Windows / macOS / Linux 终端
    ssh -L 30031:127.0.0.1:30031 <user>@<服务器地址>
    # 然后打开 http://127.0.0.1:30031

手机可以用 Termius/JuiceSSH 之类的 App 做同样的端口转发。

**方式二：VPN 内网访问（WireGuard / Tailscale）**

VPN 已把流量加密，可直接绑 VPN 地址 + token（不需要额外 TLS，但要显式允许明文）：

    # 生成并保存 token
    mkdir -p ~/.config/rbackup-ui
    openssl rand -base64 32 > ~/.config/rbackup-ui/web.token
    chmod 600 ~/.config/rbackup-ui/web.token

    # 想直连 VPN 地址时，显式指定 --web-addr（默认只绑 loopback）
    rbackup-web --web-addr 10.14.10.5:30031 \
      --web-token-file ~/.config/rbackup-ui/web.token \
      --web-insecure-http

手机连上 VPN 后访问 `http://10.14.10.5:30031`，输入 token 即可。
（`--web-insecure-http` 只在这类已加密的私有网络里使用。）

**方式三：反代 + TLS（有域名/证书时）**

Nginx/Caddy 反代到 `127.0.0.1:30031`，SSE 需关闭缓冲；**应用必须加
`--web-trust-proxy`**，否则扫码链接会生成 `http://`、cookie 不带 `Secure`、
限流与审计看到的都是代理 IP（127.0.0.1）：

    # Caddy
    rbackup.example.com {
        reverse_proxy 127.0.0.1:30031 {
            flush_interval -1
            header_up X-Forwarded-Proto {scheme}
            header_up X-Forwarded-Host {host}
        }
    }

    # Nginx（IP:端口 与 域名两个入口可共用同一 upstream）
    server {
        listen 443 ssl;
        http2 on;
        server_name backup.example.com;
        ssl_certificate     /etc/nginx/ssl/example.crt;
        ssl_certificate_key /etc/nginx/ssl/example.key;
        location / {
            proxy_pass http://127.0.0.1:30031;
            proxy_http_version 1.1;
            proxy_set_header Host              $http_host;
            proxy_set_header X-Real-IP         $remote_addr;
            proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto https;
            proxy_set_header X-Forwarded-Host  $host;
            proxy_set_header Upgrade           $http_upgrade;
            proxy_set_header Connection        "";
            proxy_buffering off;          # SSE 必须关闭缓冲
            proxy_read_timeout 3600s;
        }
    }

    启动：rbackup-web --web-trust-proxy --web-token-file ~/.config/rbackup-ui/web.token

- 不设 `--web-public-url` 时，扫码链接/同源校验按每个请求的
  `X-Forwarded-Proto` + `X-Forwarded-Host` 自适应：域名入口生成
  `https://域名`，`IP:端口` 入口生成 `https://IP:端口`，两种访问方式都正确。
- 只有确定"只从一个固定地址访问"时才设 `--web-public-url`（多入口场景反而会指错）。
- 代理必须**覆盖**这些头（不要透传客户端伪造值），否则信任了错误来源。

**方式四：内网直连自签 TLS**

    openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
      -keyout web.key -out web.crt -subj "/CN=10.14.10.5"
    rbackup-web --web-addr 10.14.10.5:8443 \
      --web-tls-cert web.crt --web-tls-key web.key \
      --web-token-file ~/.config/rbackup-ui/web.token

手机首次访问需要信任自签证书（或用方式一/二规避证书问题）。

> 监听 `0.0.0.0` 等于对所有网卡开放；没有 TLS 时程序会拒绝启动，
> 除非显式加 `--web-insecure-http`。公网暴露请务必走 TLS + token。

完整的 systemd 单元与 Nginx 双入口（域名 + 私网 IP:端口）部署见
[docs/deploy.md](docs/deploy.md)，模板在 [contrib/](contrib/)（systemd + Nginx）。

### 页面功能

- 任务列表（配置只读）、门禁模式、危险操作标记；
- 运行选中 / Dry-run 预览 / 挂载检查（手动按钮）/ 取消运行；
- SSE 实时日志与每任务统计（文件数、大小、数据量、速率）；
- 只读模式（`--web-read-only`）适合当监控页。

### 限制

- 不支持在 Web 上编辑配置：改 `config.ini` 后点「重载」；
- 历史日志/统计浏览不在本版本（v1.2 计划）；
- 一次只跑一个 job、任务串行，与 TUI 语义一致。

## 快捷键

### 全局

| 键 | 功能 |
|---|---|
| F1 / ? | 显示 / 关闭帮助 |
| Tab | 循环切焦点 (1→2→3→1) |
| 1 / 2 / 3 | 直选焦点（信息 / 任务 / 交互） |
| Ctrl+D ×3 | 强制退出程序 |

### 焦点 1：信息区（header）

| 键 | 功能 |
|---|---|
| ← → / h l | 横向滚动 |
| ↑ ↓ / j k | 纵向滚动（保留） |
| PgUp / PgDn | 翻页 |
| g / G / Home / End | 纵向首行 / 末行 |
| 0 / $ | 横向行首 / 行尾 |

### 焦点 2：任务区（table + 命令区）

| 键 | 功能 |
|---|---|
| ↑ ↓ / j k | 移动光标 |
| ← → / h l | 命令区横向滚动 |
| PgUp / PgDn | 翻页 |
| g / G / Home / End | 首行 / 末行 |
| 0 / $ | 命令区行首 / 行尾 |
| 空格 | 选择 / 取消选择 |
| a / n | 全选 / 全不选 |
| Enter | 运行选中任务 |
| d | 预览（dry-run） |
| m | 挂载检查 |
| r | 刷新配置 |

### 焦点 3：交互区（interact）

| 键 | 功能 |
|---|---|
| ↑ ↓ / j k | 滚动日志 |
| ← → / h l | 横向滚动 |
| PgUp / PgDn | 翻页 |
| g / G / Home / End | 纵向首行 / 末行 |
| 0 / $ | 横向行首 / 行尾 |
| p / 空格 | 暂停 / 继续自动滚动 |
| Esc | 切回任务区焦点 |

### 空闲状态（无运行、无确认）

| 键 | 功能 |
|---|---|
| q | 退出程序 |
| Esc | 退出程序 / 交互区焦点切回任务区 |
| Ctrl+C | 退出程序 |

### 运行中（任何焦点）

| 键 | 功能 |
|---|---|
| Ctrl+C | 停止任务 |
| p / 空格 | 暂停 / 继续自动滚动 |
| ↑↓←→ / j k h l | 滚动 |
| PgUp / PgDn | 翻页 |
| Tab | 切焦点 |

### 危险确认中（交互区焦点）

| 键 | 功能 |
|---|---|
| y | 确认当前任务 |
| n | 跳过当前任务 |
| a | 全部确认 |
| s | 全部跳过 |
| Ctrl+C | 全部跳过 |
| ↑↓←→ / PgUp / PgDn | 滚动确认详情 |

---

## 挂载门禁

### 语义

| 场景 | 源 | 目标 | 门禁 |
|---|---|---|---|
| 明文 → 明文挂载点 | 明文 | gocryptfs 挂载点 | require_mounted |
| 密文 → 密文目录 | 密文 | gocryptfs 底层 | require_unmounted |

### require_mounted

用于同步明文到已解密的挂载点。

- 目标必须已挂载，且类型匹配
- 未挂载 → 拒绝（避免明文写入未加密磁盘）

配置示例：

    require_mount=yes
    mount_path=/srv/st1000dm/Work
    mount_fstype=fuse.gocryptfs

或新键写法：

    require_mounted=yes
    mount_point=/srv/st1000dm/Work
    mount_fstype=fuse.gocryptfs

### require_unmounted

用于同步密文到 gocryptfs 底层。

- 目标必须未挂载
- 已挂载 → 拒绝（gocryptfs 正在使用底层密文，写入会破坏一致性）

配置示例：

    require_unmounted=yes
    mount_point=/mnt/wd4000g/Work
    mount_fstype=fuse.gocryptfs

### 两者互斥

同时为 yes 会报错退出（退出码 2）。

### 软链接挂载点

部分系统（如 OpenMediaVault）用软链接组织存储：

    /srv/st1000dm -> /srv/dev-disk-by-id-ata-xxx-part1

直接比较字符串会误判"未挂载"。rbackup-cli 在远端一次 SSH 完成：

1. `realpath -m <mount_point>` 解析软链接
2. `findmnt -T <真实路径>` 查询挂载信息
3. `realpath -m <TARGET>` 再解析一遍
4. 比较两个真实路径

这样即使 mount_point 和 findmnt 返回的路径写法不同，只要指向同一位置，就判定"已挂载"。

---

## 统计与日志

### 文件位置

日志和统计文件生成在同一个目录，文件名前缀相同，后缀不同：

    $XDG_STATE_HOME/rbackup-ui/log/rbackup_20260928_1922.log      完整日志
    $XDG_STATE_HOME/rbackup-ui/log/rbackup_20260928_1922.stats    统计报告

日志目录按以下顺序解析（`rbackup-ui` 采用同一套规则，否则它找不到 `.stats`）：

1. `config.ini` 里的 `LOG_DIR`（显式指定，优先级最高）
2. 平台默认目录：
   - Linux / macOS：`$XDG_STATE_HOME/rbackup-ui/log`，未设置时为 `~/.local/state/rbackup-ui/log`
   - Windows：`%LOCALAPPDATA%\rbackup-ui\Logs`
3. `<rbackup-cli 所在目录>/log`（旧行为，最后兜底）

文件名带时分（YYYYMMDD_HHMM），同一分钟内多次运行会覆盖同名文件。

想确认平台默认目录（不读配置）：

    rbackup-cli --print-log-dir

### 统计文件格式

key=value 纯文本，分组结构。全部为展示字段（英文单位），无原始字段。

单任务块：

    [task]
    name=infra_secrets_cipher
    result=success
    duration=47s
    files=8 / 463
    total_size=26.00M
    data=sent 28.97K + received 330B
    rsync=45s
    list=3s
    rate=661.60 KB/s
    prep=2s

汇总块（文件末尾）：

    [summary]
    tasks=success 2 / skipped 0 / failed 0 / mount_failed 0
    duration=3min15s
    files=16 / 926
    total_size=52.50M
    data=sent 58.25K + received 660B
    rate=305.60 KB/s
    rsync=1min36s
    list=6s

**字段说明**：

| 字段 | 含义 |
|---|---|
| result | success / skipped / failed / mount_failed / preview |
| duration | 任务总耗时（含准备） |
| files | 实际传输文件数 / 扫描文件总数 |
| total_size | 源端文件总大小 |
| data | 发送 + 接收字节 |
| rsync | rsync 执行时间 |
| list | rsync 内部文件列表生成时间 |
| rate | 平均速率（按 rsync 耗时计算） |
| prep | 准备阶段耗时（挂载检查、目录创建等） |

**时间格式**：`47s` 或 `3min15s`
**字节格式**：`B` / `K` / `M` / `G`（2 位小数）
**速率格式**：`X.XX KB/s`

### 用命令行读取统计

    # 查看所有任务名
    grep '^name=' ~/rbackup/log/rbackup_*.stats

    # 查看汇总
    grep -A 20 '\[summary\]' ~/rbackup/log/rbackup_*.stats

    # 只取失败任务
    awk '/^\[task\]/{n=""} /^name=/{n=$0} /^result=failed/{print n}' \
        ~/rbackup/log/rbackup_*.stats

### 日志与 TUI 的关系

- TUI 只显示 `rbackup-cli` 的实时输出，不解析日志文件
- 统计通过 `rbackup-cli` 输出的单行 `[STATS]` 协议传递给 TUI
- TUI 拦截 `[STATS]` 行，不显示在日志区
- `[STATS]` 行仍会写入日志文件，供命令行工具读取

### 运行时长与数据

TUI 里每个任务完成后显示 **2 行**详情：

    >>> [1/5] infra_secrets_cipher 成功    用时: 47s    累积: 成功 1  跳过 0  失败 0  挂载门禁失败 0
          传输: 文件: 8/463  总大小: 26.00M  数据: 发送 28.97K + 接收 330B  速率: 661.60 KB/s  列表: 3s  执行: 45s

最终汇总包含 **3 行**（含分隔线）：

    --------------------------------------------------------------------------------
    [2026-09-28 19:24:10] 全部任务完成（总用时 3min15s）
      成功 2: infra_secrets_cipher Work_Cipher  跳过 0  失败 0  挂载门禁失败 0
      传输: 文件: 16/926  总大小: 52.50M  数据: 发送 58.25K + 接收 660B  速率: 305.60 KB/s  列表: 6s  执行: 1min36s
    --------------------------------------------------------------------------------

rbackup-cli 日志文件中的对应输出格式一致（字段顺序：文件 → 总大小 → 数据 → 速率 → 列表 → 执行）。

### 分隔线

- **rbackup-cli 输出**：80 个短横线（`-`），用于脚本启动、任务边界、rsync 阶段分隔
- **TUI 危险确认**：80 个短横线，上下包裹确认详情
- **TUI 汇总**：80 个短横线，上下包裹汇总块

### 退出码

| 退出码 | 含义 |
|---|---|
| 0 | 全部成功（含跳过） |
| 1 | 有任务失败 |
| 2 | 参数 / 配置错误 |
| 3 | 有任务因挂载门禁失败 |

---

## 常见问题排查

### 挂载检查误报"未挂载"

**现象**：手动执行 `findmnt -T <path>` 返回正确结果，但 rbackup-cli 报"未挂载"。

**原因**：路径中含软链接，`findmnt` 返回真实路径，与配置的 mount_point 字符串不相等。

**验证**：

    ssh -p <PORT> -i <KEY> <USER>@<HOST> \
        'realpath -m /your/mount/point && \
         findmnt -rn -T $(realpath -m /your/mount/point) -o TARGET,FSTYPE'

如果 TARGET 和 realpath 输出不同，说明是软链接问题。

**解决**：rbackup-cli 已内置 realpath 解析，无需额外处理。如果仍报错，检查 rbackup-cli 是否为最新版本。

### rsync 报"failed to set times"

**现象**：

    rsync: [generator] failed to set times on "...": Operation not permitted (1)
    rsync error: some files/attrs were not transferred (see previous errors) (code 23)

**原因**：加密挂载点拒绝设置 mtime。

**解决**：GLOBAL_OPTS 加 `--omit-dir-times`（目录），必要时再加 `--no-times`（文件）：

    GLOBAL_OPTS=-avzhu --progress --no-perms --omit-dir-times --chown=admin:users --update --delete-after

### rsync 报"You can only specify a user-affecting --chown once"

**原因**：`--chown` 在 GLOBAL_OPTS 和任务 opts 里各写了一次。

**解决**：只保留一处。检查方法：

    grep -n "\-\-chown" config1.ini

### rsync 报"Permission denied (13)"

**现象**：

    rsync: [sender] send_files failed to open "...": Permission denied (13)

**原因**：运行 rbackup-cli 的用户没有权限读取源端文件。

**解决**：

- 如果是加密挂载点，先卸载 gocryptfs，改密文文件属主和权限：

      sudo chown -R <user>:<group> /path/to/cipher
      sudo chmod -R u+rwX,g+rX /path/to/cipher

- 或者用 `--sudo` 让远端 rsync 提权（仅远端，本地仍需读权限）

### 日志里中文显示为 \#345\#267\#245

**原因**：rsync 在非 UTF-8 locale 下转义非 ASCII 字符。

**解决**：

    export LC_ALL=C.UTF-8
    export LANG=C.UTF-8

或在 rsync 选项里加 `--8-bit-output`。

不影响同步，只是日志可读性问题。

---

## 构建

### 本地平台

    make build

### 交叉编译

    make build-win              # Windows exe
    make build-linux            # Linux amd64
    make build-linux-arm64      # Linux arm64
    make build-all              # 全部

### 产物位置

    bin/
    ├── windows/rbackup-tui.exe
    └── linux/rbackup-ui

### 版本注入

Makefile 会自动注入版本：

    BUILD_VERSION := $(git describe --tags --always --dirty)
    VERSION       := $(git describe --tags --abbrev=0)

- `BUILD_VERSION`：含 commit hash 和 dirty 标记，用于二进制内部（帮助浮层显示）
- `VERSION`：纯 tag，用于文件名和 Release 版本号

打 tag 后版本号显示为 `vX.Y.Z`，未打 tag 显示 `dev`。

---

## 打包与发布

### 打包

    make package

生成三个压缩包到 `dist/`：

    rbackup-ui-vX.Y.Z-linux-amd64.tar.gz
    rbackup-ui-vX.Y.Z-linux-arm64.tar.gz
    rbackup-ui-vX.Y.Z-windows-amd64.zip

每个压缩包内含：

    rbackup-ui-<平台>/
    ├── rbackup-tui (或 .exe)
    ├── rbackup-web (或 .exe；与 TUI 同一文件)
    ├── rbackup-cli
    ├── config1.ini.example
    ├── config2.ini.example
    ├── README.md
    └── LICENSE

顶层有子目录，不会污染用户当前目录。`rbackup-cli` 与二进制同目录，用户运行时自动找到。

### 发布到 GitHub Release

发布由 GitHub Actions 自动完成：**推送 tag 即触发**。

    # 1. 提交并推送代码
    git add -A
    git commit -m "feat: ..."
    git push

    # 2. 打 tag 并推送（用 -a 创建注释 tag）
    git tag -a vX.Y.Z -m "vX.Y.Z: ..."
    git push origin vX.Y.Z

    # 3. 观察 workflow
    gh run watch

`.github/workflows/release.yml` 会：

1. 校验 tag 与 `git describe` 一致（浅克隆缺 tag 会直接失败）
2. 编译 linux/amd64、linux/arm64、windows/amd64 三个平台
3. 打包成压缩包，生成 `SHA256SUMS`
4. 冒烟测试产物，复核校验和
5. 创建 Release（已存在则覆盖附件）

附件包含三个压缩包、`SHA256SUMS` 和 `install.sh`。Release 里的 `install.sh`
就是懒人模式入口，两者永远同版本。

    dist/
    ├── rbackup-ui-vX.Y.Z-linux-amd64.tar.gz
    ├── rbackup-ui-vX.Y.Z-linux-arm64.tar.gz
    ├── rbackup-ui-vX.Y.Z-windows-amd64.zip
    ├── install.sh
    ├── SHA256SUMS
    └── RELEASE_NOTES.md

### 本地打包（不上传）

    make release

等价于 `make package stage-release-extras checksums release-notes`：产物落在
`dist/`，**不会**创建或修改任何 Release，也不需要 gh。用于离线交付、内网分发
或发布前预演。

### 其他发布命令

    # 校验已发布资产（下载 + 比对 SHA256SUMS + bash -n install.sh）
    make verify-release VERSION=vX.Y.Z

    # 应急手工上传（Actions 不可用时）
    make release-upload VERSION=vX.Y.Z

    # 只生成 notes 文件查看
    make release-notes VERSION=vX.Y.Z

    # 删除 Release（保留 tag）
    make release-delete VERSION=vX.Y.Z

### 参数

| 变量 | 说明 |
|---|---|
| VERSION | 版本号，默认取 git 最近 tag |
| NOTES | notes 文件路径，默认 dist/RELEASE_NOTES.md |
| REPO | 仓库，默认从 git remote 解析 |
| EXPECT_TAG | CI 用：要求解析出的版本必须等于该 tag |

---

## 环境变量

| 变量 | 说明 | 默认 |
|---|---|---|
| RBACKUP_CONFIG | 配置文件路径 | $HOME/rbackup/config.ini |
| RBACKUP_SCRIPT | rbackup-cli 路径 | 自动查找 |
| RBACKUP_BASH | bash 可执行文件路径 | 自动查找 |
| RBACKUP_DEBUG_KEYS | 设为 1 输出按键调试日志 | — |

---

## 常见问题

### F1 无反应？

部分终端（gnome-terminal、konsole）把 F1 保留为帮助。使用 ? 代替。

### 按 F1 或 ? 后帮助浮层出现但按键无响应？

检查 RBACKUP_DEBUG_KEYS=1，按一次 F1 看 key.log。若无 Key=xxx 记录，说明按键被终端拦截。

### Windows 下按键重复触发？

MSYS2 mintty 会重复分发事件。代码内置 50ms 去重。

### Windows 下 ESC 无法退出？

MSYS2 + winpty 会吞掉 ESC。使用 q 或 Ctrl+C。

### Linux 下 rbackup-tui 找不到 bash？

设置 RBACKUP_BASH=/bin/bash，或在 config.ini 中确认 SSH_KEY 等路径正确。

### rsync 输出没显示？

先做挂载检查和远端目录检查，输出有几秒延迟。等待即可。

### 日志目录不可写？

按 `LOG_DIR` → 平台默认目录 → 脚本同目录 `log/` 的顺序回退。先用
`rbackup-cli --print-log-dir` 看平台默认目录在哪，再检查权限。

### 如何只做挂载检查？

命令行 rbackup-cli --check-mount；TUI 中按 m。

### 危险确认时怎么快速跳过全部？

按 s（no to all）或 Ctrl+C。

### 如何强制退出？

1.5 秒内连按 3 次 Ctrl+D。

### 顶部日志和统计路径显示为相对路径？

已修复。如果仍出现，检查二进制是否为最新编译。

### 统计文件去哪了？

与日志同目录，同名不同后缀。TUI 顶部显示完整路径。

### 状态栏按键提示方括号 `[a]` 显示不出来？

已修复（改用 `tview.Escape`）。

### 按 Enter / d 后 TUI 卡死？

已修复（危险确认闪烁首次渲染改为异步）。

### 命令太长显示不全？

命令区固定 2 行，超长命令自动截断。用 `←→/hl` 横滚，`0/$` 跳到行首/行尾。

### 日志行太长看不全？

用 `←→/hl` 横滚，`0/$` 跳到行首/行尾。

### 想看终端首行/末行？

纵向用 `g/G`（同 `Home/End`），横向用 `0/$`。

---

## 文件结构

    rbackup-ui/
    ├── main.go                  TUI 主程序
    ├── config.go                配置解析
    ├── runner.go                执行 rbackup-cli
    ├── rbackup-cli               备份核心脚本
    ├── install.sh               懒人模式安装器（也作为 Release 资产发布）
    ├── go.mod
    ├── go.sum
    ├── Makefile
    ├── README.md
    ├── LICENSE
    ├── config1.ini.example      示例一：明文源 → 明文挂载点
    ├── config2.ini.example      示例二：密文源 → 密文目录
    ├── .github/workflows/       release.yml（tag 自动发布）、ci.yml
    ├── docs/                    文档（含 CLEAN-HISTORY.md 运维操作单）
    ├── integrations/lazyrsync/  lazyrsync 挂载门禁集成（可选，不打包）
    ├── bin/                     编译产物（不入库）
    ├── dist/                    发布包（不入库）
    └── .staging/                打包临时目录（不入库）

---

## 作者

Jet Locke

---

## 许可证

MIT License

Copyright (c) 2026 Jet Locke

详见同目录 LICENSE 文件。

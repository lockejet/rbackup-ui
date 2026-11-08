# rbackup-ui Web UI 设计方案

> 状态：**已实现（2026-11-05）**，随 **v1.1.10** 发布；本文档保留为设计依据与实现说明。
>
> 2026-11-05 追加实现：**A 主题切换 + 手机适配**、**B 用户名密码登录**、
> **C TOTP 二次验证（可选）**，详见文末「15. 实现补充」。
>
> 实现文件：`web.go`、`web/{index.html,style.css,app.js}`、`web_test.go`、`proc_{unix,windows}.go`；
> 入口：`rbackup-web`（终端入口是 `rbackup-tui`；两者为同一文件的硬链接，argv[0] 决定模式）。
> 决策依据（2026-11-05）：远程/手机访问 **要**；历史日志浏览 **先不做**；
> Web 端改配置 **不允许**；挂载检查 **手动触发**；部署形态 **常驻服务**。

---

## 1. 目标与非目标

### 1.1 目标

1. 远程/手机上查看备份状态（任务列表、门禁状态、实时日志、统计）。
2. 无 TTY 环境下可运行（Windows 桌面、systemd 服务、容器）。
3. 受控触发备份：运行选中任务 / dry-run 预览 / 手动挂载检查 / 取消。
4. 作为常驻服务稳定运行：健康检查、优雅退出、审计日志、开机自启。
5. 单二进制、零新增依赖、不改现有 TUI 行为、不改 `config.ini` 格式、不改 `rbackup-cli`。

### 1.2 非目标（MVP 明确不做）

- 历史日志/统计浏览（留给 v1.2）。
- Web 端编辑/新增任务、改配置（配置只读；改配置请改 `config.ini` 后点「重载」）。
- 多用户、RBAC、OAuth/SSO。
- 任务并行执行、定时调度（cron/systemd timer 更适合）。
- 任意命令执行、目录浏览、文件下载。

### 1.3 设计原则

- **Web 是"观察 + 受控触发"，不是"远程 shell"**。
- **fail-closed**：任何可能把危险能力暴露到非本机的配置缺失时，拒绝启动而不是警告后继续。
- **危险操作必须可预览 + 二次确认**（`delete=yes` / `remove_source=yes` / `--sudo` 任务）。
- **不重构现有代码**：新增 `web.go` 层，复用 `config.go` / `runner.go`；未来再抽 `internal/`。

---

## 2. 总体架构

```
┌──────────────────────────────────────────────────────────────┐
│ rbackup-tui / rbackup-web（同一二进制，argv[0] 决定模式）    │
│                                                              │
│  main() ──┬── 默认: tview TUI（现有 2100 行，不动）           │
│           └── --web: Web 模式 ──────────────┐                │
│                                             ▼                │
│  ┌────────────────────────────────────────────────────────┐  │
│  │ web.go                                                 │  │
│  │  ├─ HTTP 路由 / 中间件（认证 / CSRF / 限流 / 超时）     │  │
│  │  ├─ jobManager（单 job 状态机 + context 取消）          │  │
│  │  ├─ SSE hub（多客户端广播 + ring buffer 重放）          │  │
│  │  ├─ audit logger（JSON 行 → stderr / 文件）             │  │
│  │  └─ //go:embed web/（index.html / app.js / style.css）  │  │
│  └──────────────┬─────────────────────────────────────────┘  │
│                 │ 复用（同 package main）                    │
│     ┌───────────┴──────────────┬───────────────────┐         │
│     ▼                          ▼                   ▼         │
│  config.go                 runner.go          main.go 路径工具 │
│  ParseConfig               StreamTask          findScriptPath │
│  Task/GlobalConfig         StreamDryRun        findConfigPath │
│                            StreamCheckMount    resolveLogDir  │
│                            parseStatsLine      format* 函数    │
└──────────────────────────────────────────────────────────────┘
```

### 2.1 关键复用点

| 需要的能力 | 现有实现 | 说明 |
|---|---|---|
| 读配置/任务列表 | `config.ParseConfig` | Web 只读调用 |
| 跑任务/预览/门禁 | `runner.StreamTask / StreamDryRun / StreamCheckMount` | 回调式，天然适配 SSE |
| 统计字段 | `parseStatsLine` + `TaskStats` | 直接复用解析 |
| 脚本/配置定位 | `findScriptPath` / `findConfigPath` | 与 TUI 同一套查找顺序 |
| 日志目录 | `resolveLogDir` / `appLogDir` | 审计日志也可放这里 |
| 展示格式 | `formatDuration` / `formatBytes` / `formatRate` | 前端展示由后端格式化后下发 |

### 2.2 与 TUI 的关系

- **互斥**：`--web` 启动 HTTP 服务，不启动 tview；同一进程不会同时跑两种界面。
- **代码边界**：MVP 阶段 `web.go` 与 TUI 同在 `package main`；当 Web 功能超过 TUI 一半时，
  再按 `internal/config`、`internal/runner`、`cmd/rbackup-tui`、`cmd/rbackup-web` 拆分
  （迁移路径写进 v1.2 计划，不在本次实现）。

---

## 3. 运行模式与命令行

### 3.1 启动方式

> 只要命令行出现任一 `--web-*` 选项，就会自动启用 Web 模式（`--web` 可省略）。

```bash
# 本机临时用（默认）
rbackup-web
# → http://127.0.0.1:30031

# 常驻服务（推荐：绑 loopback，由 Tailscale/WireGuard 或反代对外）
rbackup-web --web-token-file /etc/rbackup-ui/web.token   # 默认 127.0.0.1:30031

# 内网直连（必须 token + TLS）
rbackup-web --web-addr 0.0.0.0:8443 \
  --web-tls-cert /etc/rbackup-ui/web.crt --web-tls-key /etc/rbackup-ui/web.key \
  --web-token-file /etc/rbackup-ui/web.token

# 指定配置/脚本（与 TUI 相同的 -c / -s）
rbackup-web -c ~/.config/rbackup-ui/config.ini -s ~/.local/bin/rbackup-cli
```

### 3.2 参数表

| 参数 | 默认 | 说明 |
|---|---|---|
| `--web` | 关 | 启用 Web 模式（与 TUI 互斥） |
| `--web-addr` | `127.0.0.1:30031` | 监听地址；`http://` 前缀可选 |
| `--web-token-file` | 无 | token 文件（**推荐**，避免出现在 `ps` / shell history） |
| `--web-token` | 无 | 直接指定 token（不推荐，会进 `ps`） |
| `--web-tls-cert` / `--web-tls-key` | 无 | 直接 TLS；两者必须同时给 |
| `--web-insecure-http` | 关 | 显式允许"非 loopback + 明文 HTTP"（仅限隔离内网，启动时打红字警告） |
| `--web-audit-log` | `<logDir>/web-audit.log` | 审计日志文件；`-` 表示只写 stderr |
| `--web-max-clients` | 8 | SSE 客户端上限 |
| `--web-read-only` | 关 | 只读模式：隐藏所有运行/取消按钮，API 拒绝写操作（给"监控大屏"用） |

### 3.3 环境变量（便于 systemd / 容器）

| 变量 | 对应 |
|---|---|
| `RBACKUP_WEB_TOKEN` | `--web-token` |
| `RBACKUP_WEB_TOKEN_FILE` | `--web-token-file` |
| `RBACKUP_WEB_ADDR` | `--web-addr` |

### 3.4 启动前置校验（fail-closed）

| 监听地址 | token | TLS | 结果 |
|---|---|---|---|
| loopback（`127.0.0.1` / `::1`） | 无 | 无 | ✅ 启动（CSRF 防护生效） |
| loopback | 有 | 无 | ✅ 启动（推荐给反代/隧道场景） |
| 非 loopback | 无 | — | ❌ **拒绝启动**：打印生成 token 的命令 |
| 非 loopback | 有 | 无 | ❌ 拒绝，除非显式 `--web-insecure-http` |
| 非 loopback | 有 | 有 | ✅ 启动 |

- 只允许一对 `--web-tls-cert/key` 同时出现；证书加载失败 → 退出码 1。
- 端口占用 / 权限不足 → 退出码 1，stderr 说明。
- 生成 token：`head -c 32 /dev/urandom | base64 | tr -d '=+/' | cut -c1-43`（或 `openssl rand -base64 32`）。

---

## 4. 认证与安全模型

### 4.1 威胁模型

| 威胁 | 防护 |
|---|---|
| 局域网/公网扫描到端口 | 默认 loopback；非 loopback 必须 token+TLS |
| 浏览器恶意页面 CSRF（`fetch('http://127.0.0.1:30031/api/jobs')`） | 会话 Cookie `SameSite=Strict` + `Origin`/`Sec-Fetch-Site` 校验 + 写操作要求 `X-RB-CSRF` 头（跨站预检带不上） |
| token 被日志/代理记录 | token 只用 header/表单提交，**禁止**放 URL query；SSE 用会话 Cookie（EventSource 不能带自定义头） |
| token 被暴力破解 | 单 IP 认证失败限速（10 次/分钟）+ 失败审计 + 常数时间比较 |
| 误操作危险任务 | 危险任务需 `confirm:true`；UI 强制"先 dry-run 预览再执行" |
| 任意任务名注入 | 任务名必须命中 `config.Tasks` 白名单，否则 400 |
| 命令注入 | 全程 `exec.Command(bash, script, "--config", ..., "--task", name)`，无 shell 拼接 |
| 长时间占用 | 单个 job 互斥；SSE 客户端上限；服务端超时 |

### 4.2 Token 与会话

1. 服务端启动时读取 token（文件 0600 / 环境变量），做长度校验（≥32 字符）。
2. `POST /api/session`：body `{"token":"..."}` → 校验通过后 `Set-Cookie: rbs=...; HttpOnly; SameSite=Strict; Path=/api; Max-Age=28800`（HTTPS 时加 `Secure`）。会话内容用 HMAC(token) 派生，不落盘。
3. 后续所有 `/api/*` 请求校验 Cookie；`/health` 除外。
4. **不使用 `secure` 之外的自定义头做鉴权**，避免 EventSource 无法带头的问题；写操作另加 `X-RB-CSRF` 头（值来自 `GET /api/state` 下发的一次性随机数，与会话绑定）。
5. 会话过期（8h）后前端引导重新输入 token。
6. token 轮换：改文件 → 重启服务（systemd `restart`），旧会话因 HMAC 变化全部失效。

### 4.3 其他加固

- 请求体上限 1MB；JSON 严格解析（拒绝未知字段）。
- `http.Server`：`ReadHeaderTimeout=10s`、`ReadTimeout=30s`、`IdleTimeout=120s`；**不设 WriteTimeout**（SSE 长连接）。
- 响应头：`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`X-Frame-Options: DENY`、CSP（`default-src 'self'`）。
- SSE 响应加 `X-Accel-Buffering: no`（反代不缓冲）。
- 审计：每次写操作一行 JSON（时间、IP、UA、动作、任务列表、结果、耗时），写 `<logDir>/web-audit.log` + stderr。
- `--web-read-only` 模式下所有写接口返回 403。

---

## 5. 状态机与并发模型

### 5.1 Job 状态机

```
idle ──POST /api/jobs──▶ running ──┬── 全部完成 ──▶ done
                                    ├── 用户取消 ──▶ canceled
                                    └── 进程异常 ──▶ failed
running 期间：新 job 请求 → 409 busy
done/canceled/failed 后：保留最近 1 个 job 结果（含日志 ring buffer），可被 /api/state 读取
```

- job 类型：`run` / `dryrun` / `mountcheck`（后两者只读，不写备份）。
- **单 job 互斥**：备份并发会抢带宽/磁盘，且 `rbackup-cli` 日志名精确到分钟会撞名。
- 任务**串行**执行（与 TUI 一致），顺序 = 请求里 `tasks[]` 的顺序。
- 每个任务完成后推送 `task_result`（含 `[STATS]` 解析出的字段）。

### 5.2 取消与进程组

- `jobManager` 持有 `context.WithCancel`；取消 = `cancel()`。
- **关键点**：`exec.CommandContext` 只杀 `bash`，`rsync` 子进程可能残留。需要：
  - `proc_unix.go`：`SysProcAttr{Setpgid:true}` + `syscall.Kill(-pid, SIGTERM)`，5s 后 `SIGKILL`；
  - `proc_windows.go`：`CREATE_NEW_PROCESS_GROUP` + `taskkill /T /F /PID`；
  - 该 helper 放进 `runner.go` 的共享执行路径，TUI 的 Ctrl+C 也一并受益（一次改动，两处使用）。
- 取消是**终态**：`canceling`（等待子进程退出，最长 10s）→ `canceled`。

### 5.3 SSE 广播

- 单进程内 `hub`：`clients map[*client]chan event`，`sync.RWMutex`。
- 事件带单调递增 `seq`（`id:` 行）；客户端断线重连带 `Last-Event-ID` → 从 ring buffer 重放。
- ring buffer：2000 条日志 / 500 个结构化事件（超出丢最旧）；重放不到时发 `event: reset`，前端整页刷新 `GET /api/state`。
- 心跳：每 15s 发 `: ping`，防反代超时断连。
- 慢客户端：发送超过 512 条积压 → 断开该客户端（记录审计），不影响 job。
- 前端：`EventSource('/api/events')`；页面隐藏时保留连接（手机上切后台由浏览器决定）。

---

## 6. HTTP API 契约

约定：

- 前缀 `/api`；请求/响应 `application/json; charset=utf-8`（SSE 除外）。
- 错误统一：`{"error":{"code":"busy","message":"已有任务在运行"}}` + 对应 HTTP 状态码。
- 认证：除 `GET /health`、`POST /api/session` 外均需会话 Cookie。
- 写操作需 `X-RB-CSRF` 头。

### 6.1 端点一览

| 方法 | 路径 | 说明 | 认证 |
|---|---|---|---|
| GET | `/health` | 健康检查：`{"status":"ok","version":"v1.1.10","busy":false}` | 否 |
| GET | `/` 及静态资源 | 内嵌前端 | 否（页面内再登录） |
| POST | `/api/session` | 用 token 换会话 Cookie | 否（限速） |
| DELETE | `/api/session` | 退出（清 Cookie） | 是 |
| GET | `/api/state` | 配置摘要 + 任务列表 + 当前/最近 job + CSRF 值 | 是 |
| POST | `/api/jobs` | 启动 job | 是 |
| POST | `/api/jobs/current/cancel` | 取消当前 job | 是 |
| GET | `/api/jobs/current` | 当前 job 快照（轮询兜底） | 是 |
| GET | `/api/events` | SSE 事件流（`?since=<seq>` 可选） | 是 |
| POST | `/api/mountcheck` | 挂载检查（等价于 kind=mountcheck 的 job） | 是 |

### 6.2 `GET /api/state`

```json
{
  "version": "v1.1.10",
  "config": { "path": "/home/u/.config/rbackup-ui/config.ini", "script": "%h/.local/bin/rbackup-cli",
              "host": "example.com", "sshPort": "22", "sshUser": "admin",
              "policy": "skip", "logDir": "/home/u/.local/state/rbackup-ui/log" },
  "readOnly": false,
  "csrf": "b7f3…（与会话绑定，前端写操作时回传）",
  "tasks": [
    { "name": "alice", "src": "/d/alice/", "dst": "/srv/…/alice",
      "delete": "yes", "removeSource": "no", "sudo": false,
      "requireMounted": "yes", "requireUnmounted": "no", "mountPoint": "/srv/…" }
  ],
  "job": { "id": "j-20261105-1", "kind": "run", "state": "running",
           "startedAt": "2026-11-05T12:00:00+08:00", "tasks": ["alice"],
           "currentTask": "alice", "finished": 0, "total": 1 },
  "lastJob": null
}
```

### 6.3 `POST /api/jobs`

请求：

```json
{ "tasks": ["alice", "bob"], "dryRun": false, "confirm": false }
```

- `tasks` 必须是配置中的任务名，允许 1..N 个；空数组 400。
- `dryRun=true` 时忽略 `confirm`（预览不需要确认）。
- 任一任务满足 `delete=yes` / `remove_source=yes` / `sudo` 且 `confirm!=true` → **403** `{code:"confirm_required", tasks:[...]}`。
- 已有 job 运行 → **409** `{code:"busy"}`。
- 成功 → **202** `{"job":{...}}`。

### 6.4 `POST /api/mountcheck`

无参数（或 `{"tasks":[...]}` 可选，默认全部）。等价于启动 `kind=mountcheck` 的 job，结果通过 SSE `task_result` 推送（`result` 为 `mounted/unmounted/unknown/error` + 详情文本）。

> 按决策：**不做页面加载自动检查**，只由用户点按钮触发（远端 SSH 慢且可能打扰生产机）。

### 6.5 SSE 事件

```
id: 42
event: log
data: {"seq":42,"task":"alice","line":"…","stream":"stdout"}
```

| event | data 字段 | 说明 |
|---|---|---|
| `log` | `seq, task, line, stream` | 每行日志（合并 stdout/stderr，前端标色） |
| `task_start` | `seq, task, index, total, dryRun` | 任务开始 |
| `task_result` | `seq, task, result, exitCode, durationMs, stats` | 任务结束；`stats` 为 `TaskStats` 字段（files/bytes/rate…） |
| `job_state` | `seq, jobId, state, reason` | `running / canceling / done / canceled / failed` |
| `reset` | `seq` | 缓冲不足，前端刷新 state |
| `ping` | — | 注释行心跳，不占 seq |

---

## 7. 前端设计

### 7.1 技术选型

- 原生 HTML/CSS/JS，`//go:embed web/*` 打进二进制；**无 npm / 无构建步骤**。
- 页面内联 CSS 与 JS（或 `app.js` 单文件），保持"单文件可离线"的项目风格。
- 移动优先的响应式：≥900px 三栏（信息/任务/日志），窄屏单列 + 底部固定操作栏。

### 7.2 页面结构（线框）

```
桌面：
┌─────────────────────────────────────────────────────────────────────┐
│ rbackup-web       v2.0.0     [配置: …/config.ini]      [退出] [重载] │
├─────────────────────────────────────────────────────────────────────┤
│ 信息区：远端 admin@example.com:22 | 策略 skip | 日志 …/log | 状态: 空闲 │
├─────────────────────────────────────────────────────────────────────┤
│ 任务列表（只读配置）                                                  │
│ ☑ 名称     源                    目标              门禁      危险操作     │
│ ☑ alice    /d/alice/             /srv/…/alice      已挂载    删除        │
│ ☐ bob      /d/bob/               /srv/…/bob        未检查    -            │
│ [全选] [运行选中] [Dry-run 预览] [挂载检查]        [取消运行]           │
├─────────────────────────────────────────────────────────────────────┤
│ 运行日志（自动滚动 / 暂停 / 清屏）                      [错误过滤]     │
│ 12:00:01 [alice] 开始… 12:00:03 rsync … 12:03:22 成功 用时 3min21s     │
├─────────────────────────────────────────────────────────────────────┤
│ 统计：任务 1/2 | 文件 16/926 | 大小 52.50M | 数据 58.25K | 速率 305KB/s │
└─────────────────────────────────────────────────────────────────────┘

手机（<600px）：信息区折叠；任务表格 → 卡片列表；日志区占满剩余高度；
底部固定：[运行选中] [Dry-run] [更多 ▾]
```

### 7.3 交互要点

1. **登录页**：输入 token → `POST /api/session`；失败提示剩余尝试次数；成功后进入主页面。
2. **运行确认**：选中含危险属性的任务时，弹窗列出危险点，强制先点「Dry-run 预览」；
   预览成功后才启用「确认执行」（`confirm:true`）。
3. **状态同步**：`EventSource` 断线自动重连（浏览器原生 `retry`），顶部显示"正在重连…"；
   `event: reset` 时重新 `GET /api/state`。
4. **运行中**：所有写按钮禁用，显示当前任务/进度；「取消运行」需二次确认。
5. **统计**：直接展示 `task_result.stats`，字段复用后端 `formatBytes/formatRate` 的结果。
6. **重载配置**：按钮触发 `GET /api/state`（服务端每次实时 `ParseConfig`），不写文件。
7. **只读模式**（`--web-read-only`）：隐藏运行/取消/挂载检查，只显示状态与日志。

### 7.4 前端资源清单

| 文件 | 内容 |
|---|---|
| `web/index.html` | 页面骨架 + 登录页 + 弹窗模板 |
| `web/style.css` | 深色/浅色（跟随 `prefers-color-scheme`）、响应式 |
| `web/app.js` | 状态管理、fetch 封装、SSE、渲染（无框架） |

---

## 8. 持久化与运维

### 8.1 落盘内容

| 内容 | 位置 | 说明 |
|---|---|---|
| 审计日志 | `<logDir>/web-audit.log` | JSON Lines；`--web-audit-log -` 只写 stderr |
| token | 由用户提供（文件/环境变量） | 程序不生成、不保存 token |
| 会话 | 内存 + HMAC Cookie | 重启即失效（token 不变需重新登录） |
| job 结果 | 内存（保留最近 1 个） | 不落盘；历史统计按决策不做 |

### 8.2 常驻服务（systemd 示例）

```ini
# /etc/systemd/system/rbackup-web.service
[Unit]
Description=rbackup-ui Web UI（rbackup-web）
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=admin
Group=users
# token 放 0600 文件，避免出现在 ps / unit 文件里
EnvironmentFile=-/etc/rbackup-ui/web.env        # 可选：RBACKUP_WEB_TOKEN_FILE=...
ExecStart=%h/.local/bin/rbackup-web --web-trust-proxy \
  --web-addr 127.0.0.1:30031 \
  --web-token-file /etc/rbackup-ui/web.token \
  --web-audit-log %h/.local/state/rbackup-ui/log/web-audit.log
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
KillMode=mixed
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

- 启动自检失败（端口/权限/安全配置）→ 非零退出 → systemd 按 `Restart=on-failure` 循环，
  日志里能看到明确原因（避免"静默坏掉"）。
- `GET /health` 供 systemd `ExecStartPost`、外部监控或反代健康检查使用。

### 8.3 远程/手机访问的三种推荐形态

| 形态 | 配置 | 安全性 | 适用 |
|---|---|---|---|
| **A. 隧道/组网 + loopback（推荐）** | `--web-addr 127.0.0.1:30031`，用 Tailscale / WireGuard 从手机接入 | 最高（端口不暴露公网） | 个人远程 |
| B. 反代 + TLS | Caddy/Nginx 反代 `127.0.0.1:30031`，加 HSTS | 高（需要域名/证书） | 局域网或公网域名 |
| C. 内网直连 TLS | `--web-addr 0.0.0.0:8443 --web-tls-cert/key` + token | 中（内网信任模型） | 隔离内网 |

Caddy 片段：

```
rbackup.example.com {
    reverse_proxy 127.0.0.1:30031 {
        flush_interval -1        # SSE 必须关闭缓冲
    }
}
```

> **不推荐**直接把 `0.0.0.0` 明文 HTTP 暴露到公网（`--web-insecure-http` 只为内网调试保留）。

### 8.4 优雅退出

1. 收到 `SIGTERM`/`SIGINT`：停止接受新请求，广播 `job_state: canceling`。
2. 正在运行：先取消（进程组 TERM→5s→KILL），等待 ≤10s。
3. 写审计 `shutdown`；`server.Shutdown(ctx)` 等待连接关闭（≤5s）；退出码 0。
4. systemd `TimeoutStopSec=30` 兜底。

---

## 9. 代码改动清单

| 文件 | 类型 | 内容 |
|---|---|---|
| `web.go` | 新增 ≈800 行 | 路由/中间件/jobManager/SSE hub/审计/embed |
| `web/index.html`、`web/style.css`、`web/app.js` | 新增 ≈400 行 | 单页前端 |
| `web_test.go` | 新增 ≈250 行 | httptest + stub bash |
| `proc_unix.go` / `proc_windows.go` | 新增 ≈40 行 | 进程组设置/杀进程（build tag） |
| `runner.go` | 改动 ≈20 行 | 共享执行路径接入进程组（TUI 取消也受益） |
| `main.go` | 改动 ≈40 行 | flag 解析 + Web 模式分发（TUI 逻辑不动） |
| `README.md` | 改动 | 新增「Web UI」一节（启动、安全、systemd、反代） |
| `.github/workflows/ci.yml` | 改动 | Web 冒烟：起服务→登录→state→job(stub)→SSE 收到 ≥1 事件→取消 |
| `docs/WEBUI-DESIGN.md` | 本文档 | — |

**依赖**：无新增（`net/http`、`embed`、`encoding/json`、`crypto/*` 均标准库）。

---

## 10. 测试计划

### 10.1 单元/集成（`go test`）

| 用例 | 断言 |
|---|---|
| 安全前置校验 | 非 loopback 无 token → 启动失败；非 loopback 无 TLS → 失败；loopback 免 token → 成功 |
| 认证 | 错误 token 401；正确 token 换 Cookie；无 Cookie 访问 `/api/state` 401 |
| CSRF | 同源带 `X-RB-CSRF` 通过；缺头 403；`Origin` 为外站 403 |
| 任务白名单 | 未知任务名 400；空列表 400 |
| 危险确认 | `delete=yes` 任务无 `confirm` → 403 `confirm_required` |
| 互斥 | 运行中再提交 → 409 |
| SSE | 事件有序、带 seq；带 `Last-Event-ID` 能重放；ring buffer 溢出发 `reset` |
| 取消 | 长任务（stub `sleep`）取消后进程组全部退出（`pgrep` 为空） |
| 审计 | 每次写操作产生一行合法 JSON |
| 只读模式 | 写接口 403 |

测试用 stub：`testdata/stub-rbackup-cli` 支持 `--task`/`--dry-run`/`--check-mount`，
输出固定日志与 `[STATS]` 行，退出码可控（用环境变量）。

### 10.2 CI 新增

```yaml
- name: Web 冒烟
  run: go test -run 'TestWeb' -v .
```

（保持无外部依赖：httptest + stub bash，Windows runner 不跑 Web 测试，Linux runner 跑。）

### 10.3 手工验收（发布前）

1. 手机浏览器（同一 Wi-Fi）走 Tailscale 访问，完成：登录→选任务→dry-run→确认执行→看日志→取消。
2. 断开再连（SSE 重连）；刷新页面（状态恢复）。
3. 非 loopback 无 token 启动 → 确认拒绝启动；`--web-insecure-http` → 确认有红字警告。
4. systemd 启动/`systemctl restart`/`stop` 各一次，确认健康检查与优雅退出。
5. 危险任务：确认必须先 dry-run 才能执行。

---

## 11. 里程碑与版本计划

| 阶段 | 内容 | 交付 | 估计 |
|---|---|---|---|
| M1 | jobManager + 认证/CSRF 中间件 + 6 个 API + `/health` | 可 curl 跑通 | 0.5 天 |
| M2 | 单页前端（登录/任务/日志/统计/确认/响应式） | 浏览器可用 | 0.5 天 |
| M3 | 进程组取消、SSE 重放、审计、限流、systemd 示例 | 安全/运维达标 | 0.5 天 |
| M4 | 测试补齐、CI、README、发布 **v1.1.10** | 可发布 | 0.5 天 |

后续（v1.2，不在本次）：历史日志/统计浏览、统计图表、任务队列、多 token 管理、
`internal/` 拆分与独立 `rbackup-web` 二进制。

---

## 12. 风险与对策

| 风险 | 对策 |
|---|---|
| 备份是危险操作，Web 暴露放大误操作 | 确认 + dry-run 前置 + 只读模式 + 审计 |
| 取消后 rsync 残留 | 进程组 kill（Unix / Windows 分别实现）+ 集成测试断言无残留 |
| 反代缓冲导致 SSE 卡死 | `X-Accel-Buffering: no` + Caddy `flush_interval -1` + 15s 心跳 |
| 手机切后台导致连接断 | EventSource 自动重连 + Last-Event-ID 重放 |
| 长任务在反代/网关超时 | 心跳保活；前端断线重连；`/api/jobs/current` 轮询兜底 |
| Windows 进程组/信号语义差异 | `taskkill /T /F`；Windows 上 Web 冒烟不阻塞发布 |
| token 泄露 | 只走 header/表单；文件 0600；审计失败尝试；支持轮换 |
| 服务被误配置成 0.0.0.0 明文 | fail-closed + `--web-insecure-http` 显式开关 |

---

## 13. 附：错误码表

| code | HTTP | 含义 |
|---|---|---|
| `unauthorized` | 401 | 无/失效会话 |
| `invalid_token` | 401 | token 错误（限速） |
| `csrf` | 403 | 缺 CSRF 头 / Origin 不合法 |
| `confirm_required` | 403 | 危险任务缺少确认 |
| `read_only` | 403 | 只读模式拒绝写操作 |
| `unknown_task` | 400 | 任务名不在配置中 |
| `bad_request` | 400 | 参数/JSON 非法 |
| `busy` | 409 | 已有 job 运行 |
| `internal` | 500 | 未知错误（细节只进日志） |

---

## 14. 待评审确认（实现前需要你点头）

1. 会话有效期 8h 是否合适？（可配）
2. 审计日志默认写 `<logDir>/web-audit.log` 是否可接受？
3. 运行中提交新 job 是直接 409，还是允许"排队一个"？（我建议 MVP 直接 409）
4. 危险任务是否强制"必须先 dry-run 成功一次"才允许执行？（我建议是）
5. `--web-insecure-http` 是否需要？（我建议保留但默认关闭，仅内网调试）

---

## 15. 实现补充（2026-11-05）：主题 / 手机 / 密码 / TOTP

### 15.1 主题（A）

- 三态：`auto`（跟随 `prefers-color-scheme`）/ `light` / `dark`；
- 实现：`html[data-theme]` + CSS 变量，选择存 `localStorage('rbtui-theme')`，
  页面 head 内联脚本先应用主题避免闪烁；
- 入口：顶栏与登录页的「🌓」按钮。

### 15.2 手机适配（A）

- ≤720px：任务表格切换为**卡片列表**；操作按钮固定在底部（`env(safe-area-inset-bottom)`）；
- 触控目标 ≥44px；登录输入框 16px 防 iOS 缩放；日志区独立滚动；
- ≤960px 双栏变单栏，隐藏源/目标列；≤420px 进一步精简徽标列。

### 15.3 用户名密码登录（B）

- `--web-user`（默认 `admin`）+ `--web-password-hash-file`；
- 哈希：PBKDF2-HMAC-SHA256，60 万次迭代，随机 16 字节盐，格式
  `pbkdf2-sha256$<iter>$<saltB64>$<hashB64>`，常数时间比较；
- 生成：`rbackup-web --web-hash-password`（无回显交互输入）；
- 失败计数与登录限速共用（同 IP 10 次/分钟）；
- token 登录保留，作为忘记密码/认证器时的恢复方式；
- 非 loopback 下仍要求 TLS（或显式 `--web-insecure-http` + 可信 VPN）。

### 15.4 TOTP 二次验证（C，可选）

- `--web-2fa totp` + `--web-totp-secret-file`，仅对**密码登录**生效；
- 流程：密码校验通过 → 发 5 分钟有效的 HttpOnly 待验证 Cookie → `POST /api/session/totp`
  校验 6 位码（±1 窗口）→ 才发正式会话 Cookie；
- 防重放：已用过的 30s 窗口不能再次使用；待验证会话最多 5 次尝试；
- `rbackup-web --web-gen-totp` 生成 secret 与 `otpauth://` URI，终端里直接渲染二维码；
  `--web-show-totp-qr` 可对现有 secret 重新显示二维码（不轮换）；
  二维码渲染可用 `RBACKUP_NO_QR=1` 关闭、`RBACKUP_QR=1` 强制输出；
- **token 登录跳过 TOTP**（token 本身就是强凭据，也作为丢失认证器时的恢复入口）。

### 15.5 新增接口与前端

| 接口 | 说明 |
|---|---|
| `GET /api/authinfo` | 未认证可读：`{methods:["token","password"],totp:bool,readOnly,version}` |
| `POST /api/session` | body 支持 `{token}` 或 `{username,password}`；需要 2FA 时返回 `totpRequired:true` |
| `POST /api/session/totp` | body `{code}`；用待验证 Cookie 完成二次验证 |

前端登录页按 `authinfo` 自动展示 token 或用户名密码表单（两者都有时可切换），
并支持 2FA 六位码输入步骤。

### 15.6 测试

`web_test.go` 覆盖：密码哈希生成/校验、RFC 6238 测试向量、密码登录成功/失败、
TOTP 流程（要求 2FA → 未完成 401 → 错误码 401 → 正确码 200）、token 跳过 TOTP、
CLI 新参数解析、`/api/authinfo`；`web/app.js` 在 CI 里跑 `node --check`。

---

## 16. 实现补充（2026-11-05）：2FA 自助管理 / 手机扫码登录 / i18n / 日志配色

### 16.1 2FA 自助管理（服务端新增接口）

启动参数 `--web-2fa totp` 表示"策略开启"，secret 文件可不存在（初始状态）；
若存在 `--web-totp-secret-file`（或默认 `$XDG_CONFIG_HOME/rbackup-ui/web.totp`）则自动加载。
未绑定认证器时密码登录不要求 2FA，登录后前端提示一键启用。

| 接口 | 说明 |
|---|---|
| `GET /api/2fa` | 状态：`{policy, configured, backupCodes}` |
| `POST /api/2fa/setup` | 生成新 secret + otpauth URI（不落盘） |
| `POST /api/2fa/setup/confirm` | 校验 6 位码 → 原子写入 secret、生成 10 个备份码（SHA-256 哈希存储） |
| `POST /api/2fa/backup-codes` | 需当前动态码/备份码；重新生成备份码 |
| `POST /api/2fa/disable` | 需当前动态码/备份码；删除 secret 与备份码文件 |

备份码文件默认为 secret 文件 + `.backup-codes`（0600，逐行 SHA-256）；用过的备份码会被删除。
动态码统一 ±1 时间窗、防重放；管理类操作共用同一重放计数。

### 16.2 手机扫码登录

- `POST /api/phone-login`（需登录 + CSRF）：生成 32 字节一次性 token，内存仅存 SHA-256 哈希，
  返回 `{url, expiresIn}`；URL 为 `<scheme>://<host>/#t=<token>`，Host 为 loopback 时自动换成局域网 IPv4。
- `POST /api/session/phone` `{token}`：校验未过期/未使用 → 标记已用 → 下发会话；单次有效。
- TTL 120s，最多 8 个并发链接；扫码页 JS 读取 `location.hash` 后立即 `history.replaceState` 清除。

### 16.3 服务端二维码

`GET /api/qr.png?data=...`（需登录）：用 rsc.io/qr 生成二维码，纯 Go 渲染 PNG（静区 4、scale 6），
供"2FA 绑定"与"手机登录"弹窗的 `<img>` 直接使用；不依赖外部二维码服务。

### 16.4 前端：i18n / 主题 / 日志配色

- i18n：`data-i18n` / `data-i18n-ph` / `data-i18n-title` + 前端字典（zh/en），
  错误码本地化（`err.*`）；语言存 `localStorage('rbtui-lang')`，默认跟随浏览器。
- 主题：`data-theme` + CSS 变量，三态（auto/light/dark）；运行日志背景改为 `var(--card)`，
  与任务列表一致，不再固定深色。
- 安全中心/手机登录/备份码/二维码均为弹窗，移动端自适应。

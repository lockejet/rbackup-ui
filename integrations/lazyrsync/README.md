# lazyrsync × rbackup 挂载门禁接入

结论：**lazyrsync 0.3.0 本身没有任何挂载/gocryptfs 检测能力**（源码里
`gocryptfs`、`findmnt`、`mountpoint`、`fstype`、`fusermount` 零命中），也没有
pre/post 钩子。但它把「调用哪个 rsync」做成了配置项 `rsync_path`，而 TUI 运行、
TUI 预览(dry-run)、无头运行三条路径全部经由 `rsync::binary()` 取该配置。
因此**不用改 lazyrsync 一行代码**，用一个冒充 rsync 的包装器就能把 rbackup 的
门禁语义完整搬过去。

本目录提供可运行原型：

| 文件 | 作用 |
|---|---|
| `rbackup-gate.sh` | 冒充 rsync 的门禁包装器，门禁参数直接读你现有的 `config*.ini` |
| `test-gate.sh` | 自测（桩 rsync / 桩 ssh），18 项断言 |

## 1. 接入（3 步）

**第一步**，`~/.config/lazyrsync/settings.toml`：

```toml
rsync_path = "/path/to/rbackup-ui/integrations/lazyrsync/rbackup-gate.sh"
```

**第二步**，给需要门禁的任务在 `profiles.toml` 里加一个任务名标记（就是你
`config*.ini` 里 `[task_xxx]` 的 `xxx`）；门禁参数不重复维护，直接指向现有配置：

```toml
[profile.task.advanced]
raw_args = "--rbackup-task=alice --rbackup-config=/path/to/rbackup-ui/config.ini"
```

**第三步**（可选），`settings.toml` 里没有的位置用环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `RBACKUP_CONFIG` | 无 | 默认 config.ini；也可每任务用 `--rbackup-config=` 指定 |
| `RBACKUP_REAL_RSYNC` | `$PATH` 首个 `rsync` | 真正的 rsync，**支持多词**，可写 `sudo rsync`（等价于 `RSYNC_PATH=sudo rsync`） |
| `RBACKUP_SSH` | `ssh` | ssh 客户端，测试时可指向桩程序 |
| `RBACKUP_GATE_DRYRUN` | `warn` | dry-run 时 `warn`(只告警放行) / `block`(拒绝) |
| `RBACKUP_GATE_EXIT` | `40` | 门禁拒绝的退出码（rsync 官方码止于 35/255，不冲突） |

## 2. 门禁语义（与 rbackup-cli 的 `remote_mount_check2` 完全一致）

判定用 `findmnt -rn -T <realpath> -o TARGET,FSTYPE`，**要求路径本身就是挂载点**：

| 配置 | 含义 | 通过条件 |
|---|---|---|
| `require_mount=yes` | 同步的是 gocryptfs 明文视图 | `realpath == findmnt TARGET`，且 fstype 匹配 `mount_fstype` |
| `require_unmounted=yes` | 同步的是底层密文目录 | 路径不是挂载点（或不存在） |
| `MOUNT_POLICY=ignore` | 全局关闭门禁 | 直接放行 |
| 两者都 `yes` | 配置错误 | 拒绝执行 |

远端/本地自动判别：`mount_path`（或 rsync 的 dst）形如 `user@host:/path` 时，
从 lazyrsync 生成的 `--rsh=ssh -o BatchMode=yes [-p N] [-i KEY]` 里取出 ssh 参数，
在**远端**执行探测；否则在本机探测（用于源目录是本机 gocryptfs 挂载点的场景）。

## 3. 实测结果

```
$ bash test-gate.sh
...
== 15. 真机真实 gocryptfs 挂载点（若存在） ==
  PASS 明文任务放行: [GATE][OK] ... /srv/dev-disk-by-id-ata-ST1000DM003-.../Work 已挂载 (fuse.gocryptfs)
  PASS 密文任务被拒: [GATE][FAIL] 任务 'task_real_cipher' 挂载门禁拒绝（require_unmounted）
结果: 18 通过, 0 失败
```

端到端（lazyrsync 0.3.0 本体，本地 dst 便于真实落盘）：

```
$ lazyrsync list
  alice-e2e
      /home/.../rbackup-gate.sh -a -z -v -h --partial --info=progress2 \
        --rbackup-task=alice --rbackup-config=/tmp/lr-e2e/gate.ini -- <src>/ <dst>/

$ lazyrsync run gateprof
[1/2] ✔ alice-local  0.2s
[2/2] ✗ bob-blocked  exit 40  bob-e2e
2 tasks: 1 ok, 1 failed      # 退出码 40
# stderr:
[GATE][OK] alice: /tmp 已挂载 (tmpfs)
[GATE][FAIL] 任务 'bob' 挂载门禁拒绝（require_mount）
           路径: /tmp/lr-e2e/notmnt
           详情: ... 未挂载（最近挂载点 /tmp，fstype=tmpfs）
           → 目标不是解密挂载点，同步明文会写入未加密磁盘。请重新挂载 gocryptfs 后重试。
```

被拒绝的任务**没有写入任何数据**：目标目录 `notmnt/` 内容为 0 项，门禁发生在
rsync 启动之前。

## 4. 注意事项 / 与原生 rbackup 的差异

1. **门禁只在“启动 rsync 之前”生效**。lazyrsync 的 `prepare_dest()`
   （`src/rsync.rs:136`）会先为**本地**目标创建父目录，快照模式还会创建快照根目录；
   远端目标则不做任何事。所以门禁拦不住这一层 mkdir（但拦得住数据写入）。
2. **没有“跳过但不失败”语义**。rbackup 的 `MOUNT_POLICY=skip` 会把未挂载记为
   “跳过、不影响退出码”；lazyrsync 无头模式只有退出码，门禁拒绝只能表现为任务失败
   （退出码 40）。对备份场景这通常更安全，但需要知道这个差别。
3. **dry-run 默认放行**。lazyrsync 的 `p` 预览同样经过包装器，若拦截会导致预览不可用；
   默认只打印 `[GATE][DRY-RUN][WARN]`。要严格拦截设 `RBACKUP_GATE_DRYRUN=block`。
4. **stderr 才是安全通道**。预览解析的是 rsync 的 stdout，所以包装器所有提示都写
   stderr，不会污染 `+`/`~`/`-` 差异解析。
5. **`--delete` 门禁不管 raw_args**。lazyrsync 自己文档也承认：写在 `advanced.raw_args`
   里的 `--delete` 绕过它的确认门禁。本包装器同样不解析 raw_args 里的破坏性开关。
6. lazyrsync 没有 `--remove-source-files` 复选项、没有 rbackup 的
   `--backup --backup-dir=<时间戳>` 目录式备份、也不做远端父目录存在性检查
   （rbackup-cli:836-863）；这些需要靠 `raw_args` 补齐或走 fork 路线。

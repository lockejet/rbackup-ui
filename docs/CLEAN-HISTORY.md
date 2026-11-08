# 历史清理与公开化操作单

> **本文档里的命令请你自己执行。** 它们包含 `filter-repo` 重写历史与 `push --force`，
> 都是不可逆操作，不适合由自动化代劳。
>
> **已于 2026-10-31 执行过一次**（过程、校验与残留项见文末「执行记录」）。
> 下述步骤保留作参考；如需再次执行，请先按第 1 步做镜像备份。
>
> **本文件不写真实值。** 文中出现的地址一律使用 RFC 5737 文档保留网段
> （`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`），主机名一律用
> `example.com`，用户名一律用 `<user>`。

## 一句话现状（2026-10-31 更新）

| 项目 | 结论 |
|---|---|
| 仓库可见性 | **public**（`gh repo view` / GitHub API 均为 `private: false`） |
| 历史是否已重写 | **是**（2026-10-31）。新历史扫描零命中 |
| 旧对象是否已清除 | **是**（2026-11-01，方案 B：删除并重建仓库）。旧 SHA 的 raw/API/页面均 404，`git fetch <旧SHA>` 报 `not our ref` |
| 当前工作树 | 干净；真实配置（`config-*.ini`）、真实日志、私有脚本**从未进过本仓库历史** |
| 本文档自身 | 旧版本在 HEAD 里写有真实内网地址与用户名，已于 2026-10-30 脱敏 |

也就是说：**真正要清的只有"文件内容里的字符串"和一个 4.9 MB 二进制**，而不是
"删掉某个被跟踪的配置文件"。这一点和常见做法（`--invert-paths` 删文件）不同，
用错会连源码一起删掉，见第 1 步。

## 前置检查（可复现）

```bash
cd ~/rbackup-ui

# ① 历史里的内网地址（形如 192.0.2.10 / 192.0.2.10:2222）
git log --all -p | grep -oE '192\.168\.[0-9]{1,3}\.[0-9]{1,3}(:[0-9]+)?' | sort | uniq -c

# ② 历史里的本机密钥路径（只是路径，不是密钥本体）
git log --all -p | grep -oE '/home/[^/ ]+/\.ssh/[A-Za-z0-9_.-]+' | sort | uniq -c

# ③ 私钥 / 证书 / 口令 / Token（应全部为空）
git log --all -p | grep -nE 'BEGIN [A-Z ]*PRIVATE KEY|BEGIN OPENSSH|ghp_|AKIA[0-9A-Z]{16}|password=' | head

# ④ 历史里的大对象（二进制残留）
git rev-list --objects --all \
  | git cat-file --batch-check='%(objecttype) %(objectsize) %(rest)' \
  | awk '$2>1048576 {printf "%.2f MB  %s\n", $2/1048576, $3}'

# ⑤ 历史里出现过的全部路径（确认没有真实配置/日志）
git log --all --pretty=format: --name-only | sed '/^$/d' | sort -u
```

2026-10-30 的实测结果：

| 项目 | 结论 |
|---|---|
| 私钥 / 证书 | 历史中**没有**（`PRIVATE KEY`、`BEGIN OPENSSH`、`id_rsa`、`.pem` 零命中） |
| 口令 / Token | 历史中**没有**（`password=`、`passphrase`、`ghp_`、`AKIA` 零命中） |
| 内网信息 | **有**：内网地址若干处，命中 `README.md`、`config.go`、`rbackup-cli`、`config2.ini.example` |
| 本机密钥路径 | **有**：`/home/<user>/.ssh/id_ed25519` 及其变体若干处（只有路径，无密钥内容） |
| 真实配置 / 日志 / 私有脚本 | **没有**：`config-*.ini`、`rbackup.d/`、`log/`、`bin/`、`dist/`、`backup/`、`.staging/` **从未被提交** |
| 二进制残留 | **有**：历史中的 `rbackup-ui`（约 4.93 MB，初始化提交引入、下一次提交删除） |
| 当前 HEAD 泄露 | 本文件旧版本含真实内网地址与用户名（已脱敏）；其余跟踪文件无命中 |

结论：需要重写历史的只有 **4 个文本文件的内容 + 1 个二进制路径**。
`config1.ini.example` / `config2.ini.example` 是保留文件，只做内容替换，不要删。

---

## 第 1 步：备份 + 确认工作区干净（不可跳过）

`filter-repo` 会重写 refs 并重置工作区，未提交的改动会丢。

```bash
cd ~/rbackup-ui
git status --short        # 必须无输出；有改动先提交

# ① 远端镜像（含全部历史与 tag）——唯一的回滚手段
git clone --mirror git@github.com:lockejet/rbackup-ui.git \
    ~/rbackup-ui-backup-$(date +%F).git

# ② 记录清理前的 refs 指纹，便于事后比对
git tag -l --format='%(refname:short) %(objectname)' > ~/rbackup-ui-tags-before.txt
git rev-parse HEAD >> ~/rbackup-ui-tags-before.txt
```

> **不要**在这里用 `filter-repo --invert-paths --path README.md` 之类的做法：
> 本仓库的敏感内容是**写在文件里的字符串**，按路径删会把源码/文档整体删掉。
> 正确解法是 `--replace-text` 就地替换（下一步）。

## 第 2 步：清史

### 2.1 先删除二进制路径

```bash
cd ~/rbackup-ui
git filter-repo --force --invert-paths --path rbackup-ui
```

### 2.2 再替换文件内容里的敏感字符串

```bash
cat > ~/rbackup-cleanup-2026-10-31/metadata/redact-text.txt <<'EOF'
regex:192\.168\.[0-9]{1,3}\.[0-9]{1,3}(:[0-9]+)?==>192.0.2.10
regex:/home/[^/ \n]+/\.ssh/[A-Za-z0-9_.-]+==>~/.ssh/id_ed25519
EOF

git filter-repo --force --replace-text ~/rbackup-cleanup-2026-10-31/metadata/redact-text.txt
```

说明：

- 替换规则是 Python 正则，**先在一次性副本上试跑**（`git clone --mirror` 到 /tmp 再跑），
  确认 `git log -p` 里语义没被破坏。
- 想保留端口号，就把第一条写成带捕获组的版本：
  `regex:192\.168\.[0-9]{1,3}\.[0-9]{1,3}(:\d+)?==>192.0.2.10\1`
- `filter-repo` 会移除 `origin` remote，这是正常的，第 4 步加回来。

## 第 3 步：校验（必须全部为空）

```bash
# ① 内容级扫描：内网地址 / 本机密钥路径 / 私钥（应无输出）
git log --all -p | grep -nE '192\.168\.|/home/[^/ ]+/\.ssh/|BEGIN [A-Z ]*PRIVATE KEY'

# ② 二进制不应再出现在任何提交里（应无输出）
git log --all --pretty=format: --name-only | sort -u | grep -x 'rbackup-ui' || echo "✓ 二进制已清除"

# ③ 保留文件仍在（应全部存在）
git log --all --pretty=format: --name-only | sort -u \
  | grep -E '^(README\.md|config\.go|rbackup\.sh|config2\.ini\.example)$' | wc -l   # 期望 4

# ④ 体积应明显下降（清掉 4.9 MB 二进制后应小于 1 MB 量级）
git count-objects -vH

# ⑤ tag 仍在（SHA 已变）
git tag -l
```

## 第 4 步：强制推送

> **顺序很重要：先推分支，再推 tag。** 若 tag 事件先送达，此时默认分支上还没有
> `.github/workflows/release.yml`，GitHub 不会为这次 tag 推送创建 workflow 运行。
> 补推的办法是删掉 tag 再推一次：
> `git push origin :refs/tags/vX.Y.Z && git push origin refs/tags/vX.Y.Z`

```bash
git remote add origin git@github.com:lockejet/rbackup-ui.git
git push --force --all     # 先分支
git push --force --tags    # 再 tag（会触发 Release workflow）
```

推送后逐个确认 Release 仍正常（附件独立于 git 历史，通常不受影响，但 tag 的 SHA 变了）：

```bash
gh release list --repo lockejet/rbackup-ui
for t in $(git tag -l); do
    printf '%-8s ' "$t"
    gh release view "$t" --repo lockejet/rbackup-ui --json assets \
        --jq '.assets | length' 2>/dev/null || echo "无 Release"
done
```

## 第 5 步：旧对象残留（force push ≠ 删除）

重写后，按旧 SHA 匿名探测仍可能取到旧对象（GitHub 不会立即 GC）：

- `/commits/<old-sha>`、`tree/<old-sha>` 仍可能 200；
- 已 clone 的副本、fork、第三方归档不会因 force push 而消失；
- 仓库是 public 期间被爬虫抓取的内容无法收回。

若要求"彻底不可达"，需向 GitHub Support 提交清除不可达对象请求，并评估是否重建仓库。
对本次的暴露面（内网地址 + 密钥路径，且无密钥本体），更现实的目标是：
**新历史里不再有、新 clone 拿不到**，并接受旧 SHA 可发现性极低的残留。

## 第 6 步：回滚

用第 1 步的镜像恢复远端：

```bash
cd ~/rbackup-cleanup-2026-10-31/backups/rbackup-ui-mirror-backup-*.git
git push --mirror git@github.com:lockejet/rbackup-ui.git
```

## 影响与代价

1. **所有已 clone 的副本作废**：历史被重写，旧副本无法直接 push/pull，必须重新 clone。
2. **tag SHA 全部变化**：本地旧 clone 的 tag 与远端不一致，需要
   `git fetch --force --tags` 或重新 clone。
3. **commit 历史内容会变**：README/config 里的示例文字被替换为文档保留值，
   这是预期结果；不要试图保留原始明文。
4. **与代码无关的东西不要动**：真实配置与日志本就在 `.gitignore` 里，
   且从未入库，清理时无需处理（也不要把它们提交进去）。

---

## 执行记录（2026-10-31）

### 做了什么

1. **备份**（均在仓库外）：
   - 远端全量镜像：`~/rbackup-cleanup-2026-10-31/backups/rbackup-ui-mirror-backup-YYYY-MM-DD.git`（31 个提交、11 个 tag）
   - 九个 Release 的资产：`~/rbackup-cleanup-2026-10-31/backups/rbackup-release-backup/`（v1.1.1–v1.1.9，约 75 MB）
   - 清理前的 refs 指纹：`~/rbackup-cleanup-2026-10-31/metadata/refs-before.txt`
2. **在独立副本上重写**（原仓库保持不动，校验通过后才推送）：
   - pass 1：`git filter-repo --force --invert-paths --path rbackup-ui`
     （移除历史中 4.93 MB 的二进制）
   - pass 2：`git filter-repo --force --replace-text ~/rbackup-cleanup-2026-10-31/metadata/redact-text.txt`
     - `regex:192\.168\.\d+\.\d+(:\d+)?` → `192.0.2.10`
     - `regex:/home/[A-Za-z0-9_.-]+/\.ssh/[A-Za-z0-9_.-]+` → `~/.ssh/id_ed25519`
     - `regex:/home/<user>` → `~`
   - 结果：32 → **31 个提交**（一个"仅删除二进制"的空提交被裁剪）、**11 个 tag** 全部重写；
     HEAD 的 tree 哈希与重写前完全一致，即只改历史、未改当前内容
3. **推送**：`git push --force origin main` → 再 `git push --force origin --tags`
   （先分支后 tag）。推送后 CI 在重写后的 main 上通过。
4. **校验**（匿名全新 clone）：
   - `git log --all -p | grep -E '192\.168\.|/home/[^/ ]+/\.ssh/|/home/<user>'` → 零命中
   - 历史文件清单无 `rbackup-ui`；tag 11 个；HEAD tree 与重写前一致
   - 线上 Release 资产未被重建（强推 tag 不触发 push 工作流），下载校验和仍全部通过
5. **旧 Release 资产修复**（2026-10-31 晚）：
   - 发现 v1.1.2–v1.1.5 的 12 个资产内 `README.md` / `config2.ini.example` 含
     `/home/<user>/.ssh/...` 示例，且二进制带本机构建路径（`/home/<user>/go/pkg/mod/...`）
   - 用重写后的对应 tag 重新构建（`-trimpath` 去除构建路径）并覆盖上传：
     `~/rbackup-cleanup-2026-10-31/scripts/rebuild-old-releases.sh` + `~/rbackup-cleanup-2026-10-31/scripts/upload-old-releases.sh`（`gh release upload --clobber`）
   - 复核：12 个资产的文件清单与原包一致、版本标记正确、`/home/<user>` 与内网地址零命中；
     原始资产仍留底于 `~/rbackup-cleanup-2026-10-31/backups/rbackup-release-backup/`
   - 随后把 v1.1.6–v1.1.9 也重建，使二进制内嵌的 GitCommit 指向新历史：
     - v1.1.9 走 CI（`gh workflow run release.yml -f tag=v1.1.9`，39s 成功，SHA256SUMS 由 CI 重生成）
     - v1.1.6–v1.1.8 的 tag 里 `rbackup-cli --help` 仍有旧 bug，CI 冒烟会失败，
       改用本地 `-trimpath` 构建 + 重算 `SHA256SUMS`（保留原 install.sh 行）后覆盖上传，
       脚本 `~/rbackup-cleanup-2026-10-31/scripts/rebuild-1678.sh`；线上复核校验和通过、内嵌 commit 为新 SHA
   - 至此 **9 个 Release 的资产均构建自重写后的历史**

### 仍待处理

无。（2026-11-01 已用方案 B 删除并重建仓库，旧对象彻底不可达；见下节执行记录。）
2. ~~旧 Release 资产残留~~ **已处理**：v1.1.2–v1.1.5 的 12 个资产已用重写后的 tag
   重新构建并覆盖上传，线上复核无本机路径/内网地址；原始资产留底在
   `~/rbackup-cleanup-2026-10-31/backups/rbackup-release-backup/`。若需回退或改用删除方案，`~/rbackup-cleanup-2026-10-31/scripts/rbackup-release-fix.sh` 里
   有 `--delete` / `--replace` 两种模式的现成命令。
3. ~~发布二进制里的 GitCommit~~ **已处理**（2026-10-31 晚）：v1.1.6–v1.1.9 的资产已重建，
   内嵌 GitCommit 均指向重写后的新 SHA；9 个 Release 的资产现在全部构建自新历史。
   重建前的原始资产仍留底于 `~/rbackup-cleanup-2026-10-31/backups/rbackup-release-backup/`。

### 方案 B 执行记录（2026-11-01）

前置：`gh auth refresh -h github.com -s delete_repo` 补权限；`backups/restore-assets/`
里已备好 9 个 Release 的**重建后干净资产**（逐个校验过）。

1. 删除仓库 → 同名重建为 public、空仓库（`gh repo delete` + `gh repo create`）
2. 临时关闭 Actions → 推 `main` 与 11 个 tag → 重新打开 Actions
   （关闭是为了避免推 tag 时触发 4 个 Release 重建；Actions 历史随之清零，属预期代价）
3. 用 `backups/restore-assets/` 恢复 9 个 Release（标题/正文取自
   `metadata/release-metadata.json`，按版本升序创建，最后 v1.1.9 自动为 Latest）
4. 恢复仓库设置：description、`has_issues=true`、`has_projects=true`、`has_wiki=false`

复验结果：

| 检查 | 结果 |
|---|---|
| 旧提交 raw / API / 页面 | 404 / 422 / 404 |
| `git fetch origin <旧SHA>` | `not our ref`（exit 128） |
| 全新 clone 历史扫描 | 零命中，34 提交、11 tag |
| 9 个 Release | 资产齐全；v1.1.6–v1.1.9 的 `SHA256SUMS` 校验通过 |
| `releases/latest/download/install.sh` | 200 |
| 仓库设置 | 与重建前一致（stars/forks/issues 均为 0，无损失） |

已知代价：Release 的原始创建时间、Actions 历史、仓库创建时间被重置（内容与资产不受影响）。

### 备份清理（2026-11-01）

方案 B 完成并复核后，已删除全部本地备份（约 154MB），随后把整个工作目录
`~/rbackup-cleanup-2026-10-31/` 也删除了（2026-11-01）。文中的 `~/rbackup-cleanup-2026-10-31/...`
路径仅作历史记录，**现已不存在**；当前唯一事实源是 GitHub（11 个 tag、9 个 Release），
如需重新打包旧版本可按本文档的 `filter-repo`/打包步骤从 tag 重建。

### 备注

- 强推 tag **不会**触发 `Release` 工作流（本次实测：四个带 `release.yml` 的 tag
  强推后没有产生新的 run），因此历史 Release 资产保持原样。
- 旧 tag 强推后，所有人的本地副本都需要 `git fetch --force --tags` 或重新 clone。
- 2026-11-01 重建仓库后，本地仓库的 `origin` 已指向同名新仓库，`git pull/fetch` 正常；
  原旧对象只保留在本地镜像备份 `backups/rbackup-ui-mirror-backup-2026-10-31.git` 中。

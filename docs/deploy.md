# 部署指南：systemd + Nginx 双入口（域名 / 私网 IP:端口）

目标：同一套 rbackup-ui 服务，既能通过**私网 IP + 端口**访问
（如 `https://192.168.8.231:44331`），也能通过**域名**访问
（如 `https://rbackup.example.com`）；后端进程只监听 `127.0.0.1:30031`，
由 Nginx 终止 TLS 并反代。应用侧只开 `--web-trust-proxy`，**不固定
`--web-public-url`**，这样两种入口的登录 cookie（`Secure`）、扫码登录链接、
CSRF 同源校验、限流/审计客户端 IP 都能自动正确。

> 同一台机器上的 gocryptfs-ui 用完全相同的骨架，端口表：
>
> | 应用 | 后端监听 | HTTP 入口 | HTTPS 入口 | systemd 单元 |
> |---|---|---|---|---|
> | rbackup-ui | `127.0.0.1:30031` | `8031` | `44331` | `rbackup-web.service` |
> | gocryptfs-ui | `127.0.0.1:30032` | `8032` | `44332` | `gocryptfs-web.service` |
>
> 两个应用各自独立配置，互不共享凭据（token/密码/TOTP 均分开）。

## 1. 安装

```sh
curl -fsSL https://github.com/lockejet/rbackup-ui/releases/latest/download/install.sh | bash
# 或从源码：make install
```

安装后 `~/.local/bin/` 下有三个真实文件：`rbackup-tui`、`rbackup-web`
（与 tui 同一 inode 的硬链接）、`rbackup-cli`（bash 引擎，也会内嵌在二进制中，
首次运行释放到 `~/.local/share/rbackup-ui/backend/`）。

## 2. 准备凭据

```sh
mkdir -p ~/.config/rbackup-ui && chmod 700 ~/.config/rbackup-ui

# token（恢复入口，可随时轮换）
openssl rand -base64 32 > ~/.config/rbackup-ui/web.token

# 用户名密码：交互输入，不回显
~/.local/bin/rbackup-web --web-hash-password > ~/.config/rbackup-ui/web.passwd
# （或先写临时文件再 mv，避免 > 重定向先创建 0644 文件）

# 2FA：可先不建 web.totp，登录后在 Web「安全」页扫码绑定
chmod 600 ~/.config/rbackup-ui/web.token ~/.config/rbackup-ui/web.passwd
```

## 3. systemd（单一进程来源）

仓库自带模板 `contrib/systemd/rbackup-web.service`：

```sh
cd /path/to/rbackup-ui
sed -e "s|__USER__|$USER|g" -e "s|__GROUP__|$(id -gn)|g" \
    -e "s|__HOME__|$HOME|g" contrib/systemd/rbackup-web.service \
  | sudo tee /etc/systemd/system/rbackup-web.service

sudo systemctl daemon-reload
sudo systemctl enable --now rbackup-web
systemctl status rbackup-web --no-pager
```

清理旧来源（如有），避免抢端口：

```sh
# 旧的整体单元
sudo systemctl disable --now rbackup-ui 2>/dev/null || true
sudo rm -f /etc/systemd/system/rbackup-ui.service

# 用户级单元（如果之前用过 systemctl --user）
systemctl --user disable --now rbackup-ui 2>/dev/null || true
rm -f ~/.config/systemd/user/rbackup-ui.service
systemctl --user daemon-reload 2>/dev/null || true

# 手动拉起的旧进程（示例：找到后 kill）
pgrep -af 'rbackup-ui|rbackup-web' || true
```

确认只有一个进程在监听 30031：`ss -ltnp | grep 30031`。

## 4. Nginx（双入口）

模板：`contrib/nginx/rbackup-ip.conf`（IP:端口，HTTP 301 → HTTPS）与
`contrib/nginx/rbackup-domain.conf`（域名 443，Let's Encrypt/自签均可）。

```sh
cd /path/to/rbackup-ui

# 私网 IP:端口入口：替换监听地址与证书路径
sed -e 's|__LISTEN_IP__|127.0.0.1|g' \
    -e 's|__CERT__|/etc/nginx/ssl/example.crt|g' \
    -e 's|__KEY__|/etc/nginx/ssl/example.key|g' \
    contrib/nginx/rbackup-ip.conf | sudo tee /etc/nginx/sites-available/rbackup.conf

# 域名入口（可选）
sed -e 's|__DOMAIN__|rbackup.example.com|g' \
    -e 's|__CERT__|/etc/letsencrypt/live/rbackup.example.com/fullchain.pem|g' \
    -e 's|__KEY__|/etc/letsencrypt/live/rbackup.example.com/privkey.pem|g' \
    contrib/nginx/rbackup-domain.conf | sudo tee /etc/nginx/sites-available/rbackup-domain.conf

sudo ln -sf /etc/nginx/sites-available/rbackup.conf /etc/nginx/sites-enabled/
sudo ln -sf /etc/nginx/sites-available/rbackup-domain.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

要点：

- 反代必须**设置/覆盖** `X-Forwarded-Proto`、`X-Forwarded-Host`、`X-Forwarded-For`
  （模板已包含），且应用必须带 `--web-trust-proxy`；
- SSE（实时日志）需要 `proxy_buffering off` 与较长 `proxy_read_timeout`（模板已含）；
- 多个 IP / IPv6 地址按需增加 `listen` 行。

## 5. 验证清单

```sh
# 后端自身
curl -fsS http://127.0.0.1:30031/health && echo OK

# IP:端口入口（自签证书用 -k）
curl -kfsS https://192.168.8.231:44331/health && echo OK
# HTTP 必须 301 到 HTTPS
curl -sI http://192.168.8.231:8031/ | head -1

# 域名入口
curl -fsS https://rbackup.example.com/health && echo OK
```

浏览器逐项确认：

1. `https://IP:44331` 与 `https://域名` 都能登录；开发者工具里
   `Set-Cookie: rbs=...; Secure; HttpOnly; SameSite=Strict` 带 `Secure`；
2. 分别从两个入口打开「手机登录」：域名入口二维码链接形如
   `https://域名/#t=...`，IP 入口形如 `https://IP:44331/#t=...`（不应出现
   `http://` 或 `:80`）；
3. 登录失败限流、审计日志里的客户端 IP 是真实客户端而非 `127.0.0.1`；
4. SSE 实时日志不中断。

## 6. 与 gocryptfs-ui 对齐

gocryptfs-ui 仓库的 `contrib/systemd/gocryptfs-web.service` 与
`contrib/nginx/gocryptfs-*.conf` 是同一套模板（端口 30032/8032/44332），
ExecStart 只需要加 `--trust-proxy`：

```ini
ExecStart=%h/.local/bin/gocryptfs-web --listen 127.0.0.1:30032 --trust-proxy
```

两边的差异只在应用参数名（`--web-trust-proxy` vs `--trust-proxy`）与凭据文件
（各自 `~/.config/<app>/`）。域名/IP 双入口的配置方式完全一致。

## 7. 常见问题

| 现象 | 排查 |
|---|---|
| 502 Bad Gateway | 服务没起 / 端口不符：`systemctl status rbackup-web`、`ss -ltnp \| grep 30031` |
| 登录后立刻掉线 | 反代未设 `X-Forwarded-Proto: https` 或应用未开 `--web-trust-proxy`，cookie 没有 `Secure` 被浏览器丢弃；也可能是 `Host` 头与 Origin 不一致导致 403 |
| 扫码链接是 `http://` 或 `host:80` | 同上：未信任代理头（`--web-trust-proxy` + 代理覆盖 `X-Forwarded-Proto/Host`） |
| 审计/限流 IP 都是 127.0.0.1 | 未信任 `X-Forwarded-For`（检查 `--web-trust-proxy` 与 `proxy_set_header X-Forwarded-For`） |
| SSE 日志卡住 | Nginx 未关缓冲：`proxy_buffering off; proxy_read_timeout 3600s;` |
| 端口被占用 | 旧单元/手动进程残留，见第 3 节清理 |

Windows 客户端直接访问上述域名或 IP:端口即可（无需装客户端）；自签证书需先
把 CA/证书导入系统信任区，或用域名 + 正式证书。

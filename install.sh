#!/usr/bin/env bash
# ============================================================
# rbackup-ui 懒人安装器
#
#   curl -fsSL https://github.com/lockejet/rbackup-ui/releases/latest/download/install.sh | bash
#   curl -fsSL .../install.sh | sudo bash -s -- --system
#
# 不需要源码、不需要 Go。下载 Release 预编译包 → 校验 SHA256 → 安装。
# 也被 Makefile 的 install-prebuilt / install-lazy 目标复用。
# ============================================================
set -euo pipefail

# ---------- 默认值 ----------
BIN_NAME="rbackup-ui"
TUI_NAME="rbackup-tui"
WEB_NAME="rbackup-web"
SCRIPT_NAME="rbackup-cli"
REPO="${RBACKUP_REPO:-lockejet/rbackup-ui}"
VERSION="latest"
MODE="user"          # user | system
PREFIX=""            # 空 = 按 MODE 推导
FROM=""              # 本地预编译包（离线安装）
GH_TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
DO_UNINSTALL=0
DO_CONFIG=1
DO_DRY_RUN=0
REQUIRE_CHECKSUM=0
KEEP_PKG=0

# ---------- 输出 ----------
if [ -t 2 ] && [ -z "${NO_COLOR:-}" ]; then
    C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'; C_OFF=$'\033[0m'
else
    C_RED=""; C_GRN=""; C_YEL=""; C_OFF=""
fi
info()  { printf '%s\n' ">>> $*" >&2; }
ok()    { printf '%s\n' "${C_GRN}✓${C_OFF} $*" >&2; }
warn()  { printf '%s\n' "${C_YEL}!${C_OFF} $*" >&2; }
die()   { printf '%s\n' "${C_RED}✗ $*${C_OFF}" >&2; exit 1; }

usage() {
    cat >&2 <<EOF
rbackup-ui 安装器

用法:
  install.sh [选项]

级别:
  --user              用户级安装（默认）
  --system            系统级安装（Linux 需 sudo；Windows 用 %LOCALAPPDATA%）
  --prefix DIR        自定义安装前缀（覆盖上面的推导）

版本 / 来源:
  --version VER       指定版本，如 v1.1.5（默认 latest）
  --repo OWNER/REPO   指定仓库（默认 $REPO）
  --from FILE         用本地预编译包安装，不联网
  --keep-pkg          保留下载的压缩包（打印路径）

校验:
  --require-checksum  缺少 SHA256SUMS 时直接失败（默认仅告警）
  --insecure          完全跳过校验和

其他:
  --no-config         不生成配置骨架
  --uninstall         卸载（读安装清单，保留配置与日志）
  --dry-run           只打印将要做什么
  -h, --help          本帮助

环境变量:
  GH_TOKEN / GITHUB_TOKEN   私有仓库下载用；公开仓库无需
  RBACKUP_REPO              等价于 --repo
  NO_COLOR                  关闭彩色输出
EOF
}

# ---------- 参数解析 ----------
while [ $# -gt 0 ]; do
    case "$1" in
        --user)              MODE="user" ;;
        --system)            MODE="system" ;;
        --prefix)            PREFIX="${2:?--prefix 需要参数}"; shift ;;
        --prefix=*)          PREFIX="${1#*=}" ;;
        --version)           VERSION="${2:?--version 需要参数}"; shift ;;
        --version=*)         VERSION="${1#*=}" ;;
        --repo)              REPO="${2:?--repo 需要参数}"; shift ;;
        --repo=*)            REPO="${1#*=}" ;;
        --from)              FROM="${2:?--from 需要参数}"; shift ;;
        --from=*)            FROM="${1#*=}" ;;
        --keep-pkg)          KEEP_PKG=1 ;;
        --require-checksum)  REQUIRE_CHECKSUM=1 ;;
        --insecure)          REQUIRE_CHECKSUM=0; SKIP_CHECKSUM=1 ;;
        --no-config)         DO_CONFIG=0 ;;
        --uninstall)         DO_UNINSTALL=1 ;;
        --dry-run)           DO_DRY_RUN=1 ;;
        -h|--help)           usage; exit 0 ;;
        *)                   die "未知参数: $1（用 --help 查看用法）" ;;
    esac
    shift
done

# ---------- 平台探测 ----------
case "$(uname -s)" in
    Linux)                      OS="linux" ;;
    MINGW*|MSYS*|CYGWIN*|Windows_NT) OS="windows" ;;
    Darwin)                     die "暂无 macOS 预编译包，请用源码安装：make install" ;;
    *)                          die "不支持的系统: $(uname -s)" ;;
esac
case "$(uname -m)" in
    x86_64|amd64)   ARCH="amd64" ;;
    aarch64|arm64)  ARCH="arm64" ;;
    *)              die "不支持的架构: $(uname -m)" ;;
esac
if [ "$OS" = "windows" ]; then
    EXE=".exe"; PKG_EXT="zip"
else
    EXE=""; PKG_EXT="tar.gz"
fi
[ "$OS" = "linux" ] && [ "$ARCH" = "arm64" ] && :   # 明确支持

# ---------- 安装位置 ----------
if [ -z "$PREFIX" ]; then
    if [ "$OS" = "windows" ]; then
        if [ "$MODE" = "system" ]; then
            PREFIX="${LOCALAPPDATA:-$HOME/AppData/Local}/Programs/rbackup-ui"
        else
            PREFIX="$HOME/.local"
        fi
    elif [ "$MODE" = "system" ]; then
        PREFIX="/usr/local"
    else
        PREFIX="$HOME/.local"
    fi
fi
case "$PREFIX" in
    /*) ;;
    [A-Za-z]:[\\/]*) ;;
    *) PREFIX="$PWD/$PREFIX" ;;
esac

BINDIR="$PREFIX/bin"
SHAREDIR="$PREFIX/share/$BIN_NAME"

# 应用数据（示例 / 文档 / 安装清单）：遵循 XDG_DATA_HOME
if [ "$MODE" = "user" ]; then
    DATADIR="${XDG_DATA_HOME:-$HOME/.local/share}/$BIN_NAME"
else
    DATADIR="$SHAREDIR"
fi
# 安装清单与版本放在 share/<app>/install/（运行期数据 backend/ 与之分开）
METADIR="$DATADIR/install"
MANIFEST="$METADIR/install-manifest.txt"
# 改名兼容：旧版 rbackup-tui 布局与 v1 的扁平清单位置
LEGACY_MANIFEST="${DATADIR%/*}/rbackup-tui/install-manifest.txt"
LEGACY_MANIFEST_V1="$DATADIR/install-manifest.txt"

# 配置目录：程序用 os.UserConfigDir() 解析（Linux $XDG_CONFIG_HOME 或 ~/.config，
# Windows %AppData%），这里复刻同一规则，保证安装器写的位置程序读得到。
if [ "$OS" = "windows" ]; then
    _appdata="${APPDATA:-$HOME/AppData/Roaming}"
    if command -v cygpath >/dev/null 2>&1; then
        _appdata="$(cygpath -u "$_appdata" 2>/dev/null || printf '%s' "$_appdata")"
    fi
    CONFIGDIR="$_appdata/$BIN_NAME"
else
    CONFIGDIR="${XDG_CONFIG_HOME:-$HOME/.config}/$BIN_NAME"
fi

# 系统级安装：配置属于"调用 sudo 的那个用户"，不能写进 root 的家目录
# （Windows 没有 sudo 概念，--system 只是装到 %LOCALAPPDATA% 下的独立目录，
#   配置仍按当前用户的 %AppData%）
CONFIG_OWNER=""
if [ "$MODE" = "system" ] && [ "$OS" != "windows" ]; then
    _target_user="${SUDO_USER:-}"
    if [ -n "$_target_user" ] && [ "$_target_user" != "root" ]; then
        _target_home="$(getent passwd "$_target_user" 2>/dev/null | cut -d: -f6)"
        if [ -n "$_target_home" ] && [ -d "$_target_home" ]; then
            CONFIGDIR="$_target_home/.config/$BIN_NAME"
            CONFIG_OWNER="$_target_user"
        else
            CONFIGDIR=""    # 解析不到目标用户 → 不写配置，只打印指引
        fi
    else
        CONFIGDIR=""
    fi
fi

if [ "$MODE" = "system" ] && [ "$OS" = "linux" ] && [ "$(id -u)" -ne 0 ] && [ "$DO_DRY_RUN" -eq 0 ]; then
    die "系统级安装需要 root，请用：curl -fsSL <url>/install.sh | sudo bash -s -- --system"
fi

# ---------- 工具函数 ----------
need_cmd() { command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"; }

http_get() { # $1=url $2=输出文件("-" = stdout)
    local url="$1" out="$2"
    local -a auth=()
    [ -n "$GH_TOKEN" ] && auth=(-H "Authorization: Bearer $GH_TOKEN")
    if command -v curl >/dev/null 2>&1; then
        if [ "$out" = "-" ]; then curl -fsSL "${auth[@]}" "$url"; else curl -fsSL "${auth[@]}" -o "$out" "$url"; fi
    elif command -v wget >/dev/null 2>&1; then
        if [ "$out" = "-" ]; then wget -qO- "$url"; else wget -qO "$out" "$url"; fi
    else
        die "需要 curl 或 wget"
    fi
}

download() { # $1=url $2=目标文件
    info "下载 $(basename "$2")"
    http_get "$1" "$2" || die "下载失败: $1"
    [ -s "$2" ] || die "下载到的文件为空: $1"
}

# 私有仓库无法用浏览器下载 URL 取资产，需经 API 资产地址。
# 依次尝试 python3 → gh → 纯 grep 启发式，成功则打印 API URL。
api_asset_url() { # $1=资产文件名
    local name="$1" api="https://api.github.com/repos/$REPO/releases/tags/$VERSION"
    local json="" url="" line=""
    local -a auth=()
    [ -n "$GH_TOKEN" ] && auth=(-H "Authorization: Bearer $GH_TOKEN")

    if command -v curl >/dev/null 2>&1; then
        json="$(curl -fsSL "${auth[@]}" "$api" 2>/dev/null || true)"
    elif command -v wget >/dev/null 2>&1; then
        json="$(wget -qO- --header="Authorization: Bearer $GH_TOKEN" "$api" 2>/dev/null || true)"
    fi
    [ -n "$json" ] || return 0

    if command -v python3 >/dev/null 2>&1; then
        url="$(printf '%s' "$json" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for a in d.get("assets", []):
    if a.get("name") == sys.argv[1]:
        print(a.get("url", ""))
        break
' "$name" 2>/dev/null || true)"
    fi
    if [ -z "$url" ] && command -v gh >/dev/null 2>&1; then
        url="$(gh api "repos/$REPO/releases/tags/$VERSION" \
               --jq ".assets[] | select(.name==\"$name\") | .url" 2>/dev/null || true)"
    fi
    if [ -z "$url" ]; then
        line="$(printf '%s' "$json" | tr -d '\n' | sed 's/},{/}\n{/g' \
                | grep -F "\"name\": \"$name\"" | head -n1 || true)"
        url="$(printf '%s' "$line" \
               | grep -o 'https://api\.github\.com/repos/[^"]*/releases/assets/[0-9]*' | head -n1 || true)"
    fi
    printf '%s' "$url"
}

asset_url() { # $1=资产文件名；带 token 时优先 API
    local name="$1" u=""
    if [ -n "$GH_TOKEN" ]; then
        u="$(api_asset_url "$name" || true)"
        if [ -n "$u" ]; then printf '%s' "$u"; return 0; fi
    fi
    printf '%s' "$BASE_URL/$name"
}

fetch_asset() { # $1=资产文件名 $2=目标文件
    local name="$1" dest="$2" url
    url="$(asset_url "$name")"
    [ -n "$url" ] || return 1
    info "下载 $name"
    case "$url" in
        */releases/assets/*)
            local -a auth=()
            [ -n "$GH_TOKEN" ] && auth=(-H "Authorization: Bearer $GH_TOKEN")
            if command -v curl >/dev/null 2>&1; then
                curl -fsSL "${auth[@]}" -H "Accept: application/octet-stream" -o "$dest" "$url"
            else
                http_get "$url" "$dest"
            fi
            ;;
        *)  http_get "$url" "$dest" ;;
    esac
    [ -s "$dest" ]
}

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1;     then shasum -a 256 "$1" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1;    then openssl dgst -sha256 "$1" | awk '{print $NF}'
    else return 1; fi
}

resolve_latest() {
    local api="https://api.github.com/repos/$REPO/releases/latest" tag
    local -a auth=()
    [ -n "$GH_TOKEN" ] && auth=(-H "Authorization: Bearer $GH_TOKEN")
    if command -v curl >/dev/null 2>&1; then
        tag=$(curl -fsSL "${auth[@]}" "$api" 2>/dev/null \
              | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1) || true
        if [ -z "$tag" ]; then
            # 兜底：跟随 /releases/latest 的跳转
            tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
                  "https://github.com/$REPO/releases/latest" 2>/dev/null | sed 's|.*/tag/||') || true
        fi
    else
        tag=$(wget -qO- "$api" 2>/dev/null \
              | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1) || true
    fi
    [ -n "$tag" ] || die "无法获取最新版本（私有仓库请设置 GH_TOKEN；或用 --version 指定）"
    printf '%s' "$tag"
}

read_installed_version() {
    if [ -f "$MANIFEST" ]; then
        sed -n 's/^# version=//p' "$MANIFEST" | head -n1
    elif [ -f "$LEGACY_MANIFEST_V1" ]; then
        sed -n 's/^# version=//p' "$LEGACY_MANIFEST_V1" | head -n1
    fi
}

# ---------- 卸载 ----------
if [ "$DO_UNINSTALL" -eq 1 ]; then
    if [ ! -f "$MANIFEST" ] && [ -f "$LEGACY_MANIFEST_V1" ]; then
        warn "未找到新布局清单，检测到 v1 清单，按旧清单卸载"
        MANIFEST="$LEGACY_MANIFEST_V1"
    fi
    if [ ! -f "$MANIFEST" ] && [ -f "$LEGACY_MANIFEST" ]; then
        warn "未找到新布局清单，检测到改名前的 rbackup-tui 清单，按旧清单卸载"
        MANIFEST="$LEGACY_MANIFEST"
    fi
    [ -f "$MANIFEST" ] || die "找不到安装清单：$MANIFEST（可能是源码安装，请用 make uninstall）"
    info "按清单卸载（版本 $(read_installed_version)）"
    removed=0
    while IFS= read -r line; do
        case "$line" in ''|'#'*) continue ;; esac
        f="${line%%$'\t'*}"
        [ -n "$f" ] || continue
        if [ -e "$f" ]; then
            if [ "$DO_DRY_RUN" -eq 1 ]; then
                info "将删除 $f"
            else
                rm -f "$f" && info "已删除 $f"
            fi
            removed=$((removed + 1))
        fi
    done < "$MANIFEST"
    if [ "$DO_DRY_RUN" -eq 0 ]; then
        # 只删空目录；含配置/日志的目录会保留
        rmdir "$BINDIR" 2>/dev/null || true
        rmdir "$DATADIR/examples" 2>/dev/null || true
        rm -f "$MANIFEST" "$LEGACY_MANIFEST_V1" "$METADIR/VERSION"
        rmdir "$METADIR" 2>/dev/null || true
        ok "卸载完成，共移除 $removed 个文件"
        info "配置与日志已保留：${CONFIGDIR:-$HOME/.config/$BIN_NAME}"
    fi
    exit 0
fi

# ---------- 取包 ----------
TMPDIR_INST="$(mktemp -d "${TMPDIR:-/tmp}/rbackup-install.XXXXXX")"
cleanup() {
    if [ "$KEEP_PKG" -eq 1 ] && [ -n "${PKG_PATH:-}" ] && [ -f "${PKG_PATH:-}" ]; then
        info "已保留安装包: $PKG_PATH"
    else
        rm -rf "$TMPDIR_INST"
    fi
}
trap cleanup EXIT

if [ -n "$FROM" ]; then
    [ -f "$FROM" ] || die "本地包不存在: $FROM"
    PKG_PATH="$FROM"
    info "使用本地包 $FROM"
    if [ "$VERSION" = "latest" ]; then
        # 从包名反推版本：rbackup-ui-<ver>-<os>-<arch>.tar.gz|zip
        VERSION="$(basename "$FROM" | sed -n \
            's/^rbackup-ui-\(.*\)-\(linux\|windows\)-\(amd64\|arm64\)\.\(tar\.gz\|zip\)$/\1/p')"
        [ -n "$VERSION" ] || VERSION="local"
    fi
    info "版本: $VERSION"
else
    if [ "$VERSION" = "latest" ]; then
        info "解析最新版本（仓库 $REPO）"
        VERSION="$(resolve_latest)"
    fi
    case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
    info "版本: $VERSION  平台: $OS-$ARCH"
    PKG_NAME="$BIN_NAME-$VERSION-$OS-$ARCH.$PKG_EXT"
    BASE_URL="https://github.com/$REPO/releases/download/$VERSION"
    PKG_PATH="$TMPDIR_INST/$PKG_NAME"

    DOWNLOAD_FAILED=0
    fetch_asset "$PKG_NAME" "$PKG_PATH" || DOWNLOAD_FAILED=1
    if [ "$DOWNLOAD_FAILED" -eq 1 ]; then
        if [ -n "$GH_TOKEN" ]; then
            die "下载失败：$BASE_URL/$PKG_NAME（请确认该版本存在此平台资产）"
        elif [ "$REPO" = "lockejet/rbackup-ui" ]; then
            die "下载失败：$BASE_URL/$PKG_NAME
    若仓库尚未公开，请设置 GH_TOKEN，例如：
      GH_TOKEN=\$(gh auth token) curl -fsSL ... | bash"
        else
            die "下载失败：$BASE_URL/$PKG_NAME"
        fi
    fi

    # 校验和
    if [ "${SKIP_CHECKSUM:-0}" -eq 1 ]; then
        warn "已按 --insecure 跳过校验和"
    else
        SUMS_PATH="$TMPDIR_INST/SHA256SUMS"
        if fetch_asset "SHA256SUMS" "$SUMS_PATH" 2>/dev/null && [ -s "$SUMS_PATH" ]; then
            want="$(awk -v n="$PKG_NAME" '$2==n || $2=="*"n {print $1; exit}' "$SUMS_PATH")"
            if [ -z "$want" ]; then
                warn "SHA256SUMS 中无 $PKG_NAME 条目，跳过校验"
            else
                got="$(sha256_of "$PKG_PATH")" || die "本机无 sha256 工具，无法校验"
                if [ "$want" != "$got" ]; then
                    die "校验和不匹配！
      期望 $want
      实际 $got
    包可能损坏或被篡改，已中止安装。"
                fi
                ok "SHA256 校验通过"
            fi
        elif [ "$REQUIRE_CHECKSUM" -eq 1 ]; then
            die "该 Release 缺少 SHA256SUMS，已按 --require-checksum 中止"
        else
            warn "该 Release 无 SHA256SUMS，跳过校验（旧版本正常现象）"
        fi
    fi
fi

# ---------- 解包 ----------
EXTRACT="$TMPDIR_INST/extract"
mkdir -p "$EXTRACT"
case "$PKG_PATH" in
    *.tar.gz|*.tgz)
        need_cmd tar
        tar xzf "$PKG_PATH" -C "$EXTRACT"
        ;;
    *.zip)
        if command -v unzip >/dev/null 2>&1; then
            unzip -q "$PKG_PATH" -d "$EXTRACT"
        elif command -v python3 >/dev/null 2>&1; then
            python3 -m zipfile -e "$PKG_PATH" "$EXTRACT"
        elif command -v powershell.exe >/dev/null 2>&1; then
            powershell.exe -NoProfile -Command \
                "Expand-Archive -Force -LiteralPath '$(cygpath -w "$PKG_PATH" 2>/dev/null || printf '%s' "$PKG_PATH")' -DestinationPath '$(cygpath -w "$EXTRACT" 2>/dev/null || printf '%s' "$EXTRACT")'" >/dev/null
        else
            die "解压 .zip 需要 unzip / python3 / powershell 之一"
        fi
        ;;
    *) die "不认识的包格式: $PKG_PATH" ;;
esac

SRC_TUI="$(find "$EXTRACT" -type f -name "$TUI_NAME$EXE" | head -n1)"
[ -n "$SRC_TUI" ] || die "包内找不到 $TUI_NAME$EXE（v2 起主程序拆分为 $TUI_NAME / $WEB_NAME；旧包请用旧版安装器）"
SRC_WEB="$(find "$EXTRACT" -type f -name "$WEB_NAME$EXE" | head -n1)"
SRC_ROOT="$(dirname "$SRC_TUI")"
SRC_SCRIPT="$SRC_ROOT/$SCRIPT_NAME"
[ -f "$SRC_SCRIPT" ] || warn "包内没有 $SCRIPT_NAME，仅安装主程序"

# ---------- 安装 ----------
installed_ver="$(read_installed_version)"
if [ -n "$installed_ver" ] && [ "$installed_ver" != "$VERSION" ]; then
    info "升级：$installed_ver → $VERSION"
elif [ -n "$installed_ver" ] && [ "$installed_ver" = "$VERSION" ]; then
    info "已安装 $VERSION，重新安装（幂等）"
fi

if [ "$DO_DRY_RUN" -eq 1 ]; then
    info "[dry-run] 安装目录:"
    info "[dry-run]   $BINDIR/$TUI_NAME$EXE"
    info "[dry-run]   $BINDIR/$WEB_NAME$EXE（与 TUI 共享同一实现的硬链接）"
    info "[dry-run]   $BINDIR/$SCRIPT_NAME"
    info "[dry-run]   $METADIR/{VERSION,install-manifest.txt}"
    info "[dry-run]   $DATADIR/examples/{config1,config2}.ini.example"
    info "[dry-run]   $DATADIR/{README.md,LICENSE}"
    if [ "$DO_CONFIG" -eq 1 ]; then
        if [ -n "$CONFIGDIR" ]; then
            info "[dry-run]   $CONFIGDIR/config.ini（不存在才创建）"
        else
            info "[dry-run]   配置骨架：跳过（系统级安装无法确定目标用户，将打印手工指引）"
        fi
    fi
    exit 0
fi

mkdir -p "$BINDIR" "$DATADIR/examples" "$METADIR"
# 升级/替换：先删旧名（v1 的 rbackup-ui、rbackup.sh）与目标文件，避免残留、
# 避免 cp 写穿旧的符号链接/硬链接。
rm -f "$BINDIR/$TUI_NAME$EXE" "$BINDIR/$WEB_NAME$EXE" \
      "$BINDIR/$BIN_NAME$EXE" "$BINDIR/rbackup.sh" "$LEGACY_MANIFEST_V1"
install -m 0755 "$SRC_TUI" "$BINDIR/$TUI_NAME$EXE"
# web 与 tui 是同一实现的两种界面：优先硬链接（同一 inode，磁盘不翻倍）
if ln -f "$BINDIR/$TUI_NAME$EXE" "$BINDIR/$WEB_NAME$EXE" 2>/dev/null; then
    :
elif [ -n "$SRC_WEB" ] && [ -f "$SRC_WEB" ]; then
    install -m 0755 "$SRC_WEB" "$BINDIR/$WEB_NAME$EXE"
else
    cp -f "$BINDIR/$TUI_NAME$EXE" "$BINDIR/$WEB_NAME$EXE"
fi
[ -f "$SRC_SCRIPT" ] && install -m 0755 "$SRC_SCRIPT" "$BINDIR/$SCRIPT_NAME"

copied=("$BINDIR/$TUI_NAME$EXE" "$BINDIR/$WEB_NAME$EXE")
[ -f "$BINDIR/$SCRIPT_NAME" ] && copied+=("$BINDIR/$SCRIPT_NAME")

for f in config1.ini.example config2.ini.example; do
    if [ -f "$SRC_ROOT/$f" ]; then
        install -m 0644 "$SRC_ROOT/$f" "$DATADIR/examples/$f"
        copied+=("$DATADIR/examples/$f")
    fi
done
for f in README.md LICENSE; do
    if [ -f "$SRC_ROOT/$f" ]; then
        install -m 0644 "$SRC_ROOT/$f" "$DATADIR/$f"
        copied+=("$DATADIR/$f")
    fi
done

# ---------- 配置骨架：只补不覆盖 ----------
CONFIG_PATH=""
if [ "$DO_CONFIG" -eq 1 ]; then
    if [ -z "$CONFIGDIR" ]; then
        warn "系统级安装：未确定目标用户，跳过配置骨架"
        info "请让每个用户自行执行一次，或手工创建 ~/.config/$BIN_NAME/config.ini"
        info "样例：$DATADIR/examples/config2.ini.example"
    else
        CONFIG_PATH="$CONFIGDIR/config.ini"
        mkdir -p "$CONFIGDIR"
        if [ -n "$CONFIG_OWNER" ]; then
            chown "$CONFIG_OWNER" "$CONFIGDIR" 2>/dev/null || true
        fi
        # 改名兼容：新配置不存在时，尝试从旧 rbackup-tui 配置复制一份
        LEGACY_CONFIG="${CONFIGDIR%/*}/rbackup-tui/config.ini"
        if [ ! -f "$CONFIG_PATH" ] && [ -f "$LEGACY_CONFIG" ]; then
            install -m 0644 "$LEGACY_CONFIG" "$CONFIG_PATH"
            [ -n "$CONFIG_OWNER" ] && chown "$CONFIG_OWNER" "$CONFIG_PATH" 2>/dev/null || true
            warn "已从旧配置复制：$LEGACY_CONFIG -> $CONFIG_PATH（请确认内容）"
        fi
        if [ ! -f "$CONFIG_PATH" ]; then
            if [ -f "$DATADIR/examples/config2.ini.example" ]; then
                install -m 0644 "$DATADIR/examples/config2.ini.example" "$CONFIG_PATH"
                if [ -n "$CONFIG_OWNER" ]; then
                    chown "$CONFIG_OWNER" "$CONFIG_PATH" 2>/dev/null || true
                fi
                ok "已生成配置骨架 $CONFIG_PATH（请修改其中的 HOST / SSH_KEY）"
            fi
        else
            info "已存在配置，未覆盖：$CONFIG_PATH"
        fi
    fi
fi

# ---------- 安装清单 ----------
{
    printf '# rbackup-ui install manifest\n'
    printf '# version=%s\n' "$VERSION"
    printf '# mode=%s\n' "$MODE"
    printf '# date=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    for f in "${copied[@]}"; do printf '%s\n' "$f"; done
} > "$MANIFEST"
printf '%s\n' "$VERSION" > "$METADIR/VERSION"

# ---------- 收尾 ----------
ok "安装完成：$BIN_NAME $VERSION（$MODE 级）"
printf '\n' >&2
info "TUI      $BINDIR/$TUI_NAME$EXE"
info "Web      $BINDIR/$WEB_NAME$EXE"
info "后端 CLI $BINDIR/$SCRIPT_NAME"
[ -n "$CONFIG_PATH" ] && info "配置     $CONFIG_PATH"
info "示例文档 $DATADIR"
info "清单     $MANIFEST"
printf '\n' >&2

case ":$PATH:" in
    *":$BINDIR:"*) ;;
    *)
        warn "$BINDIR 不在 PATH 中，请加入 shell 配置："
        # shellcheck disable=SC2016  # $PATH 此处是给用户 shell 配置的字面文本
        printf '\n    export PATH="%s:$PATH"\n\n' "$BINDIR" >&2
        ;;
esac

info "启动：$TUI_NAME（终端）或 $WEB_NAME --web-trust-proxy（网页，建议由 systemd 托管）"
info "卸载：$BIN_NAME 安装器 --uninstall   或重跑 install.sh --uninstall"

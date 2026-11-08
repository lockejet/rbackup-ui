#!/usr/bin/env python3
"""生成 rbackup-ui 的界面示意图。

一份网格数据，两个产物：
  1. docs/images/ui-overview.svg  —— 几何边框 + 文本，SVG 矢量图（README 用）
  2. 标准输出                      —— 等宽纯文本版（README 的 <details> 折叠块用）

之所以自己排版而不是截屏：CJK 是双宽，终端 ASCII 图在浏览器里对不齐；
这里按"显示列"计算坐标，两个产物都严格对齐，且不含任何真实主机/任务名。

用法：
    python3 tools/gen-ui-preview.py            # 打印纯文本版
    python3 tools/gen-ui-preview.py --write    # 同时写入 SVG
"""
from __future__ import annotations

import html
import pathlib
import sys
import unicodedata

# ---------- 版式 ----------
INNER = 88          # 框内显示列数
CELL_W = 7.5        # 每列像素宽 = 字号/2（CJK 等宽字体正好 2:1，textLength 不产生形变）
LINE_H = 21.0       # 行高
PAD = 14            # 外边距
FONT_SIZE = 15.0
FONT_STACK = ('ui-monospace,SFMono-Regular,Menlo,Consolas,'
              '"Noto Sans Mono CJK SC","Noto Sans CJK SC","DejaVu Sans Mono",monospace')
ARIA_LABEL = "rbackup-ui 界面示意图"

# ---------- 配色 ----------
C = {
    "bg":     "#0d1117",
    "border": "#3fb950",
    "title":  "#7ee787",
    "label":  "#8b949e",
    "val":    "#e6edf3",
    "hdr":    "#8b949e",
    "name":   "#e6edf3",
    "path":   "#c9d1d9",
    "ok":     "#3fb950",
    "err":    "#f85149",
    "dim":    "#6e7681",
    "cmd":    "#79c0ff",
    "log":    "#c9d1d9",
    "loghi":  "#58a6ff",
    "bar":    "#161b22",
    "barkey": "#c9d1d9",
    "barval": "#8b949e",
    "barhi":  "#7ee787",
}


def dw(s: str) -> int:
    """显示宽度：CJK 全角算 2 列。"""
    return sum(2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in s)


# ---------- 网格数据 ----------
# flow : 顺序拼接的片段 [(文本, 样式)]，文本里自带分隔空格
# cols : 固定列 [(列号, 文本, 样式)]
# top/sep/bottom : 边框行
ROWS = [
    {"t": "top", "title": "rbackup"},

    {"t": "flow", "segs": [
        ("脚本: ", "label"), ("~/.local/bin/rbackup-cli", "val"),
        ("   配置: ", "label"), ("~/.config/rbackup-ui/config.ini", "val")]},
    {"t": "flow", "segs": [
        ("远端: ", "label"), ("admin@example.com:22", "val"),
        ("   策略: ", "label"), ("skip", "ok"), ("（门禁失败时跳过）", "dim")]},
    {"t": "flow", "segs": [
        ("日志: ", "label"), ("~/.local/state/rbackup-ui/log/20260928_1922.log", "val")]},
    {"t": "flow", "segs": [
        ("统计: ", "label"), ("~/.local/state/rbackup-ui/log/20260928_1922.stats", "val")]},

    {"t": "sep", "title": "[2] 任务列表"},

    {"t": "cols", "cells": [
        (2, " ", "dim"), (4, "任务名", "hdr"), (20, "源", "hdr"),
        (54, "目标", "hdr"), (76, "门禁", "hdr")]},
    {"t": "cols", "cells": [
        (2, "●", "ok"), (4, "alice", "name"), (20, "/d/alice/my_company/", "path"),
        (54, "/srv/.../doc-alice", "path"), (76, "已挂载", "ok")]},
    {"t": "cols", "cells": [
        (2, "●", "ok"), (4, "bob", "name"), (20, "/d/bob/my_company/", "path"),
        (54, "/srv/.../doc-bob", "path"), (76, "已挂载", "ok")]},
    {"t": "cols", "cells": [
        (2, "○", "dim"), (4, "charlie", "name"), (20, "/d/charlie/DevOps", "path"),
        (54, "/srv/st1000dm", "path"), (76, "—", "dim")]},
    {"t": "cols", "cells": [
        (2, "○", "dim"), (4, "Test1", "name"), (20, "~/rsync/test1.d/", "path"),
        (54, "/srv/st1000dm/t1", "path"), (76, "已挂载", "ok")]},
    {"t": "cols", "cells": [
        (2, "○", "dim"), (4, "Test2", "name"), (20, "~/rsync/test2.d/", "path"),
        (54, "/srv/st1000dm/t2", "path"), (76, "未挂载", "err")]},
    {"t": "blank"},

    {"t": "flow", "segs": [("> ", "loghi"), ("命令: ", "label"),
                           ("rsync -avzhu --progress --delete --exclude='/.deleted_files/'", "cmd")]},
    {"t": "flow", "segs": [("  ", "dim"),
                           ("--backup --backup-dir=\"/srv/.../rbackup/<ts>\" -e \"ssh -p 22 -i ...\" ...", "cmd")]},

    {"t": "sep", "title": "[3] 交互区"},

    {"t": "flow", "segs": [("[2026-09-28 19:24:10] ", "loghi"), ("开始实际执行：共 2 个任务", "log")]},
    {"t": "flow", "segs": [(">>> [1/2] ", "loghi"), ("alice 成功", "ok"),
                           ("    用时: 47s    累积: 成功 1  跳过 0  失败 0  挂载门禁失败 0", "log")]},
    {"t": "flow", "segs": [("     传输: ", "label"),
                           ("文件: 8/463  总大小: 26.00M  数据: 发送 28.97K + 接收 330B  速率: 661.60 KB/s", "log")]},
    {"t": "flow", "segs": [("...", "dim")]},

    {"t": "sep", "title": ""},

    {"t": "flow", "style_bar": True, "segs": [
        ("运行中   进度 2/5   当前 bob   ", "barhi"),
        ("成功 1 跳过 0 失败 0 挂载门禁失败 0   用时 1min32s", "barkey")]},
    {"t": "flow", "style_bar": True, "segs": [
        ("滚动: ", "barkey"), ("移动[↑↓/jk] 横滚[←→/hl] 翻页[PgUp/PgDn]", "barval"),
        ("  选择: ", "barkey"), ("勾选[空格] 全选[a] 清空[n]", "barval")]},
    {"t": "flow", "style_bar": True, "segs": [
        ("全局: ", "barkey"), ("切换[Tab] 直选[1/2/3] 停止[Ctrl+C] 退出[q] 帮助[F1/?]", "barval")]},

    {"t": "bottom"},
]


# ---------- 校验 ----------
def row_width(row: dict) -> int:
    t = row["t"]
    if t in ("top", "sep", "bottom"):
        return INNER
    if t == "blank":
        return 0
    if t == "flow":
        return sum(dw(txt) for txt, _ in row["segs"])
    if t == "cols":
        return max(col + dw(txt) for col, txt, _ in row["cells"])
    raise ValueError(t)


def check() -> None:
    bad = False
    for i, row in enumerate(ROWS):
        w = row_width(row)
        if w > INNER:
            print(f"✗ 第 {i} 行超宽：{w} > {INNER}  {row}", file=sys.stderr)
            bad = True
    # cols 行检测重叠
    for i, row in enumerate(ROWS):
        if row["t"] != "cols":
            continue
        spans = sorted((c, c + dw(txt)) for c, txt, _ in row["cells"])
        for (s1, e1), (s2, _) in zip(spans, spans[1:]):
            if e1 > s2:
                print(f"✗ 第 {i} 行列重叠：{s1}-{e1} 与 {s2}", file=sys.stderr)
                bad = True
    if bad:
        sys.exit(1)


# ---------- 纯文本版 ----------
def ascii_art() -> str:
    out = []
    for row in ROWS:
        t = row["t"]
        if t == "top":
            head = f"┌─ {row['title']} "
            out.append(head + "─" * (INNER - dw(head)) + "┐")
        elif t == "bottom":
            out.append("└" + "─" * INNER + "┘")
        elif t == "sep":
            title = f" {row['title']} " if row["title"] else ""
            head = "├─" + title
            out.append(head + "─" * (INNER - dw(head)) + "┤")
        elif t == "blank":
            out.append("│" + " " * INNER + "│")
        elif t == "flow":
            line = "".join(txt for txt, _ in row["segs"])
            out.append("│" + line + " " * (INNER - dw(line)) + "│")
        elif t == "cols":
            buf = [" "] * INNER
            for col, txt, _ in row["cells"]:
                x = col
                for ch in txt:
                    buf[x] = ch
                    if dw(ch) == 2:
                        if x + 1 < INNER:
                            buf[x + 1] = ""
                        x += 2
                    else:
                        x += 1
            out.append("│" + "".join(buf) + "│")
    return "\n".join(out)


# ---------- SVG ----------
def esc(s: str) -> str:
    """文本节点转义"""
    return html.escape(s, quote=False)


def esc_attr(s: str) -> str:
    """属性值转义：字体栈里的双引号必须变成 &quot;，否则 XML 非法"""
    return html.escape(s, quote=True)


def svg() -> str:
    n = len(ROWS)
    w = (INNER + 2) * CELL_W + PAD * 2
    h = n * LINE_H + PAD * 2
    x0, y0 = PAD, PAD
    x1, y1 = PAD + (INNER + 2) * CELL_W, PAD + n * LINE_H
    tx0 = x0 + CELL_W  # 内容起始 x（跳过左边框一列）

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w:.0f}" height="{h:.0f}" '
        f'viewBox="0 0 {w:.0f} {h:.0f}" role="img" '
        f'aria-label="{esc_attr(ARIA_LABEL)}" xml:space="preserve" '
        f'font-family="{esc_attr(FONT_STACK)}" font-size="{FONT_SIZE}">',
        f'<rect x="0" y="0" width="{w:.0f}" height="{h:.0f}" rx="8" fill="{C["bg"]}"/>',
        f'<rect x="{x0 + .5:.1f}" y="{y0 + .5:.1f}" width="{x1 - x0 - 1:.1f}" '
        f'height="{y1 - y0 - 1:.1f}" rx="4" fill="none" stroke="{C["border"]}" stroke-width="1"/>',
    ]

    def text(x: float, y: float, s: str, fill: str, width: float) -> str:
        if not s.strip():
            return ""
        return (f'<text x="{x:.1f}" y="{y:.1f}" fill="{fill}" '
                f'textLength="{width:.1f}" lengthAdjust="spacingAndGlyphs">{esc(s)}</text>')

    # 状态栏底色
    bar_rows = [i for i, r in enumerate(ROWS) if r.get("style_bar")]
    if bar_rows:
        by0 = y0 + min(bar_rows) * LINE_H
        bh = (max(bar_rows) - min(bar_rows) + 1) * LINE_H
        parts.append(f'<rect x="{x0 + 1:.1f}" y="{by0:.1f}" width="{x1 - x0 - 2:.1f}" '
                     f'height="{bh:.1f}" fill="{C["bar"]}"/>')

    for i, row in enumerate(ROWS):
        ty = y0 + i * LINE_H + LINE_H * 0.72
        t = row["t"]

        if t == "top":
            line_y = y0 + 0.5
            parts.append(f'<line x1="{x0}" y1="{line_y:.1f}" x2="{x1}" y2="{line_y:.1f}" '
                         f'stroke="{C["border"]}" stroke-width="1"/>')
            ttl = f"─ {row['title']} "
            tw = dw(ttl) * CELL_W
            parts.append(f'<rect x="{tx0}" y="{line_y - 8:.1f}" width="{tw:.1f}" height="16" fill="{C["bg"]}"/>')
            parts.append(text(tx0, ty, ttl, C["title"], tw))
        elif t == "bottom":
            line_y = y1 - 0.5
            parts.append(f'<line x1="{x0}" y1="{line_y:.1f}" x2="{x1}" y2="{line_y:.1f}" '
                         f'stroke="{C["border"]}" stroke-width="1"/>')
        elif t == "sep":
            line_y = y0 + i * LINE_H + 0.5
            parts.append(f'<line x1="{x0}" y1="{line_y:.1f}" x2="{x1}" y2="{line_y:.1f}" '
                         f'stroke="{C["border"]}" stroke-width="1"/>')
            if row["title"]:
                ttl = f"─ {row['title']} "
                tw = dw(ttl) * CELL_W
                parts.append(f'<rect x="{tx0}" y="{line_y - 8:.1f}" width="{tw:.1f}" height="16" fill="{C["bg"]}"/>')
                parts.append(text(tx0, ty, ttl, C["title"], tw))
        elif t == "flow":
            col = 0
            for txt, style in row["segs"]:
                seg_w = dw(txt) * CELL_W
                parts.append(text(tx0 + col * CELL_W, ty, txt, C[style], seg_w))
                col += dw(txt)
        elif t == "cols":
            for c, txt, style in row["cells"]:
                parts.append(text(tx0 + c * CELL_W, ty, txt, C[style], dw(txt) * CELL_W))

    parts.append("</svg>")
    return "\n".join(p for p in parts if p) + "\n"


def main() -> None:
    check()
    if "--write" in sys.argv:
        out = pathlib.Path("docs/images/ui-overview.svg")
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(svg())
        print(f"已写入 {out}（{out.stat().st_size} 字节）", file=sys.stderr)
    else:
        print(ascii_art())


if __name__ == "__main__":
    main()

#!/usr/bin/env bash
# 门禁包装器自测：用桩 rsync / 桩 ssh 验证判定逻辑，无需真实远程主机。
# 语义与 rbackup-cli 的 remote_mount_check2 一致：
#   require_mount      = 该路径“本身就是挂载点”，且 fstype 匹配（gocryptfs 明文视图）
#   require_unmounted  = 该路径“本身不是挂载点”（直接同步密文目录时用）
set -uo pipefail

DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
GATE="$DIR/rbackup-gate.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

cat > "$TMP/stub-rsync" <<'EOF'
#!/usr/bin/env bash
for a in "$@"; do [ "$a" = "--version" ] && { echo "rsync  version 3.5.0  protocol version 32"; exit 0; }; done
printf '%s\n' "$*" > "${STUB_RSYNC_OUT:?}"
exit 0
EOF
chmod +x "$TMP/stub-rsync"

cat > "$TMP/stub-ssh" <<'EOF'
#!/usr/bin/env bash
cmd="${!#}"          # 最后一个参数是远端命令；在本机执行以模拟远端主机
bash -c "$cmd"
EOF
chmod +x "$TMP/stub-ssh"

mkdir -p "$TMP/plain" "$TMP/notmnt"

# 自动找一个"本身即挂载点"的路径及其 fstype（CI 上 /tmp 通常不是挂载点）
detect_mount_point() {
    local cand info tgt fst rtgt rpath
    for cand in /dev/shm /proc /sys/fs/cgroup /run /; do
        [ -d "$cand" ] || continue
        info="$(findmnt -rn -T "$cand" -o TARGET,FSTYPE 2>/dev/null)" || continue
        [ -n "$info" ] || continue
        tgt="$(printf '%s' "$info" | awk '{print $1}')"
        fst="$(printf '%s' "$info" | awk '{print $2}')"
        rtgt="$(realpath -m "$tgt" 2>/dev/null || printf '%s' "$tgt")"
        rpath="$(realpath -m "$cand" 2>/dev/null || printf '%s' "$cand")"
        if [ "$rtgt" = "$rpath" ]; then
            printf '%s|%s\n' "$cand" "$fst"
            return 0
        fi
    done
    return 1
}
if ! MP_INFO="$(detect_mount_point)"; then
    echo "SKIP: 本机找不到可用作夹具的挂载点，无法运行门禁测试" >&2
    exit 0
fi
MP="${MP_INFO%%|*}"
MP_FSTYPE="${MP_INFO##*|}"
MISMATCH_FSTYPE="fuse.gocryptfs"
case "$MP_FSTYPE" in *gocryptfs*) MISMATCH_FSTYPE="ext4" ;; esac
echo "夹具挂载点: $MP ($MP_FSTYPE)  非挂载点: $TMP/notmnt"
echo

cat > "$TMP/gate.ini" <<EOF
MOUNT_POLICY=skip
HOST=fakehost

[task_need_mount_unmounted_dir]
require_mount=yes
mount_path=$TMP/notmnt
mount_fstype=fuse.gocryptfs

[task_mount_ok]
require_mount=yes
mount_path=$MP
mount_fstype=$MP_FSTYPE

[task_fstype_mismatch]
require_mount=yes
mount_path=$MP
mount_fstype=$MISMATCH_FSTYPE

[task_need_unmounted]
require_unmounted=yes
mount_path=$MP

[task_unmounted_ok]
require_unmounted=yes
mount_path=$TMP/notmnt

[task_missing_path_mount]
require_mount=yes
mount_path=$TMP/does-not-exist

[task_missing_path_unmount]
require_unmounted=yes
mount_path=$TMP/does-not-exist

[task_remote_notmnt]
require_mount=yes
mount_path=$TMP/remote-notmnt

[task_remote_ok]
require_mount=yes
mount_path=$MP
mount_fstype=$MP_FSTYPE

[task_no_gate]
opts=--update
EOF

export RBACKUP_CONFIG="$TMP/gate.ini"
export RBACKUP_REAL_RSYNC="$TMP/stub-rsync"
export RBACKUP_SSH="$TMP/stub-ssh"
export STUB_RSYNC_OUT="$TMP/rsync-args.txt"

PASS=0; FAIL=0
run_gate() { # $1=task $2=expect_exit $3=label  [extra args...]
    local task="$1" want="$2" label="$3"; shift 3
    local out rc
    out="$("$GATE" -a -z --rbackup-task="${task#task_}" "$@" -- /local/src "admin@fakehost:/srv/st1000dm/Work" 2>&1)"; rc=$?
    if [ "$rc" -eq "$want" ]; then
        PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-30s exit=%s\n' "$label" "$rc"
    else
        FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-30s exit=%s (want %s)\n' "$label" "$rc" "$want"
        printf '       %s\n' "$out" | head -3
    fi
}

echo "== 1. require_mount：路径不是挂载点 → 拒绝 =="
run_gate task_need_mount_unmounted_dir 40 "未挂载拒绝"
echo "== 2. require_mount + fstype 匹配，路径本身是挂载点 → 放行 =="
run_gate task_mount_ok 0 "fstype 匹配放行"
echo "== 3. require_mount + fstype 不符 → 拒绝 =="
run_gate task_fstype_mismatch 40 "fstype 不符拒绝"
echo "== 4. require_unmounted：路径已挂载 → 拒绝 =="
run_gate task_need_unmounted 40 "已挂载拒绝"
echo "== 5. require_unmounted：非挂载点子目录 → 放行 =="
run_gate task_unmounted_ok 0 "未挂载放行"
echo "== 6. 路径不存在 =="
run_gate task_missing_path_mount 40 "require_mount 拒绝"
run_gate task_missing_path_unmount 0 "require_unmounted 放行"
echo "== 7. dry-run(-n)：只告警不拦截 =="
run_gate task_need_mount_unmounted_dir 0 "dry-run 不拦截" -n
echo "== 7b. 任务名写法容忍：--rbackup-task 带/不带 task_ 前缀 =="
out="$("$GATE" -a --rbackup-task=task_need_mount_unmounted_dir -- /s admin@h:/d 2>&1)"; rc=$?
[ "$rc" -eq 40 ] && { PASS=$((PASS+1)); echo "  PASS 全节名写法也被识别"; } || { FAIL=$((FAIL+1)); echo "  FAIL 全节名写法 rc=$rc"; }
echo "== 8. 未配门禁的任务 → 直接放行 =="
run_gate task_no_gate 0 "无门禁放行"
echo "== 9. 远端形态：走 ssh 桩在远端探测 =="
run_gate task_remote_notmnt 40 "远端未挂载拒绝"
run_gate task_remote_ok 0 "远端已挂载放行"
echo "== 10. 无 --rbackup-task → 完全透传 =="
"$GATE" -a -- /local/src "admin@fakehost:/srv/x" >/dev/null 2>&1 \
    && { PASS=$((PASS+1)); echo "  PASS 透传"; } || { FAIL=$((FAIL+1)); echo "  FAIL 透传"; }
echo "== 11. --version 透传（lazyrsync 启动探测依赖它） =="
v="$("$GATE" --version)"; case "$v" in "rsync  version 3.5.0"*) PASS=$((PASS+1)); echo "  PASS version: $v";; *) FAIL=$((FAIL+1)); echo "  FAIL version: $v";; esac
echo "== 12. 私有参数已剥离，未泄漏给真 rsync =="
"$GATE" -a --rbackup-task=task_no_gate --rbackup-config="$TMP/gate.ini" -- /s "admin@h:/d" >/dev/null 2>&1
if grep -q -- "--rbackup" "$STUB_RSYNC_OUT"; then FAIL=$((FAIL+1)); echo "  FAIL 参数泄漏: $(cat "$STUB_RSYNC_OUT")"; else PASS=$((PASS+1)); echo "  PASS 参数干净: $(cat "$STUB_RSYNC_OUT")"; fi
echo "== 13. MOUNT_POLICY=ignore → 不检查 =="
sed 's/^MOUNT_POLICY=skip/MOUNT_POLICY=ignore/' "$TMP/gate.ini" > "$TMP/ignore.ini"
"$GATE" -a --rbackup-task=task_need_mount_unmounted_dir --rbackup-config="$TMP/ignore.ini" -- /s "admin@h:/d" >/dev/null 2>&1 \
    && { PASS=$((PASS+1)); echo "  PASS ignore 放行"; } || { FAIL=$((FAIL+1)); echo "  FAIL ignore"; }
echo "== 14. 拒绝时的诊断输出 =="
"$GATE" -a --rbackup-task=task_need_unmounted -- /s "admin@fakehost:/srv/x" 2>&1 >/dev/null | sed 's/^/       /'

echo "== 15. 真机真实 gocryptfs 挂载点（若存在） =="
GC="/srv/st1000dm/Work"
if findmnt -rn -T "$GC" -o TARGET,FSTYPE 2>/dev/null | grep -q fuse.gocryptfs; then
    cat > "$TMP/real.ini" <<EOF
MOUNT_POLICY=skip
[task_real_plain]
require_mount=yes
mount_path=$GC
mount_fstype=fuse.gocryptfs
[task_real_cipher]
require_unmounted=yes
mount_path=$GC
mount_fstype=fuse.gocryptfs
EOF
    o1="$("$GATE" -a --rbackup-task=task_real_plain --rbackup-config="$TMP/real.ini" -- /s "admin@fakehost:$GC" 2>&1)"; r1=$?
    o2="$("$GATE" -a --rbackup-task=task_real_cipher --rbackup-config="$TMP/real.ini" -- /s "admin@fakehost:$GC" 2>&1)"; r2=$?
    if [ "$r1" -eq 0 ] && [ "$r2" -eq 40 ]; then
        PASS=$((PASS+2))
        echo "  PASS 明文任务放行: $(printf '%s' "$o1" | head -1)"
        echo "  PASS 密文任务被拒: $(printf '%s' "$o2" | head -1)"
    else
        FAIL=$((FAIL+1)); echo "  FAIL 真实挂载点 r1=$r1 r2=$r2"; printf '%s\n%s\n' "$o1" "$o2" | head -6
    fi
else
    echo "  SKIP 本机无 $GC 的 gocryptfs 挂载"
fi

echo
echo "结果: $PASS 通过, $FAIL 失败"
[ "$FAIL" -eq 0 ]

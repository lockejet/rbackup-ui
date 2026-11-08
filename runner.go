package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TaskStats struct {
	FilesTransferred int
	FilesTotal       int
	BytesTotal       int64
	BytesSent        int64
	BytesReceived    int64
	RsyncMS          int64
	ListMS           int64
	PrepMS           int64
}

type TaskResult struct {
	TaskName string
	ExitCode int
	Err      error
	Stats    *TaskStats
}

// 解析 rbackup-cli 输出的 [STATS] 行
func parseStatsLine(line string) *TaskStats {
	if !strings.HasPrefix(line, "[STATS] ") {
		return nil
	}
	s := &TaskStats{}
	fields := strings.Fields(line)
	for _, f := range fields {
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], kv[1]
		switch k {
		case "files":
			parts := strings.SplitN(v, "/", 2)
			if len(parts) == 2 {
				s.FilesTransferred, _ = strconv.Atoi(parts[0])
				s.FilesTotal, _ = strconv.Atoi(parts[1])
			}
		case "bytes_total":
			s.BytesTotal, _ = strconv.ParseInt(v, 10, 64)
		case "bytes_sent":
			s.BytesSent, _ = strconv.ParseInt(v, 10, 64)
		case "bytes_recv":
			s.BytesReceived, _ = strconv.ParseInt(v, 10, 64)
		case "rsync_ms":
			s.RsyncMS, _ = strconv.ParseInt(v, 10, 64)
		case "list_ms":
			s.ListMS, _ = strconv.ParseInt(v, 10, 64)
		case "prep_ms":
			s.PrepMS, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	return s
}

// ---------- 查找 bash / 运行环境适配 ----------

// bashPathOverride 来自 --bash 参数（优先级最高）。
var bashPathOverride string

// findBash 按 CLI 参数 > RBACKUP_BASH > 常见安装路径 > PATH 的顺序查找 bash。
//
// Windows 上必须跳过 C:\Windows\System32\bash.exe：那是 WSL 的启动器，
// 不是 Git Bash/MSYS2。它一旦被当成 bash 使用，依赖探测（rsync/ssh/sed/awk）
// 会跑进 WSL 环境里，路径也应该是 /mnt/c/... 而不是 /c/...，备份任务会失败。
// 因此常见 Git/MSYS2 路径优先于 PATH 查找。
func findBash() (string, error) {
	if bashPathOverride != "" {
		return bashPathOverride, nil
	}
	if p := os.Getenv("RBACKUP_BASH"); p != "" {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		for _, c := range windowsBashCandidates() {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	}
	if p, err := exec.LookPath("bash"); err == nil {
		if !isWSLLauncher(p) {
			return p, nil
		}
		return "", fmt.Errorf("只找到 WSL 的 bash（%s），它不是 Git Bash/MSYS2，"+
			"无法直接执行备份任务。请安装 Git for Windows 或 MSYS2，"+
			"或用 --bash 指定 bash.exe 的完整路径", p)
	}
	return "", fmt.Errorf("未找到 bash。Windows 请安装 Git for Windows 或 MSYS2，" +
		"或设置 RBACKUP_BASH / 使用 --bash 指定 bash.exe 路径")
}

// windowsBashCandidates 返回 Windows 上常见的 Git for Windows / MSYS2 bash 路径。
// 用环境变量拼路径，兼容 Program Files 装在非 C: 盘、以及当前用户级安装。
func windowsBashCandidates() []string {
	var out []string
	seen := make(map[string]bool)
	add := func(p string) {
		if p == "" {
			return
		}
		key := strings.ToLower(p)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, p)
	}
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if dir := os.Getenv(env); dir != "" {
			add(filepath.Join(dir, "Git", "bin", "bash.exe"))
			add(filepath.Join(dir, "Git", "usr", "bin", "bash.exe"))
		}
	}
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		add(filepath.Join(dir, "Programs", "Git", "bin", "bash.exe"))
		add(filepath.Join(dir, "Programs", "Git", "usr", "bin", "bash.exe"))
	}
	add(`C:\msys64\usr\bin\bash.exe`)
	add(`D:\msys64\usr\bin\bash.exe`)
	return out
}

// isWSLLauncher 判断路径是否为 Windows 自带的 WSL 启动器（System32\bash.exe）。
// 纯字符串判断，便于在非 Windows 上做单元测试。
func isWSLLauncher(p string) bool {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"))
	s = strings.TrimRight(s, "/")
	const suffix = "/bash.exe"
	if !strings.HasSuffix(s, suffix) {
		return false
	}
	dir := strings.TrimSuffix(s, suffix)
	return strings.HasSuffix(dir, "/system32") || strings.HasSuffix(dir, "/sysnative")
}

// toMSYSPath 把 Windows 盘符路径转换为 MSYS2/Git Bash 的 /c/... 形式。
// 非盘符路径只把反斜杠换成正斜杠；UNC 路径保持原样（bash 下通常不可直接用）。
func toMSYSPath(p string) string {
	if p == "" || strings.HasPrefix(p, "/") {
		return p
	}
	if strings.HasPrefix(p, `\\`) { // UNC
		return p
	}
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(p[:1])
		rest := strings.ReplaceAll(p[2:], `\`, "/")
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return "/" + drive + rest
	}
	return strings.ReplaceAll(p, `\`, "/")
}

// adaptBashPath 仅在 Windows 上把路径转换成 bash 可识别形式；其他平台原样返回。
func adaptBashPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	return toMSYSPath(p)
}

// probeCommand 通过 bash 检测命令是否存在（用于 --doctor 与启动提示）。
func probeCommand(bash, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "-c", "command -v "+name+" >/dev/null 2>&1")
	return cmd.Run() == nil
}

// ---------- 对外接口 ----------

func StreamTask(ctx context.Context, scriptPath, configPath, taskName string, onLine func(string)) *TaskResult {
	args := []string{"--config", adaptBashPath(configPath), "--task", taskName}
	return streamExec(ctx, scriptPath, args, taskName, onLine)
}

func StreamCheckMount(ctx context.Context, scriptPath, configPath string, onLine func(string)) *TaskResult {
	args := []string{"--config", adaptBashPath(configPath), "--check-mount"}
	return streamExec(ctx, scriptPath, args, "__check_mount__", onLine)
}

func StreamDryRun(ctx context.Context, scriptPath, configPath, taskName string, onLine func(string)) *TaskResult {
	args := []string{"--config", adaptBashPath(configPath), "--task", taskName, "--dry-run"}
	return streamExec(ctx, scriptPath, args, taskName, onLine)
}

// ---------- 核心执行 ----------

func streamExec(ctx context.Context, scriptPath string, args []string, name string, onLine func(string)) *TaskResult {
	bash, err := findBash()
	if err != nil {
		return &TaskResult{TaskName: name, ExitCode: -1, Err: err}
	}

	// 脚本路径验证
	if scriptPath == "" {
		return &TaskResult{TaskName: name, ExitCode: -1, Err: fmt.Errorf("脚本路径为空")}
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return &TaskResult{TaskName: name, ExitCode: -1,
			Err: fmt.Errorf("脚本不存在: %s", scriptPath)}
	}

	fullArgs := append([]string{adaptBashPath(scriptPath)}, args...)
	cmd := exec.CommandContext(ctx, bash, fullArgs...)
	setProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return &TaskResult{TaskName: name, ExitCode: -1, Err: err}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return &TaskResult{TaskName: name, ExitCode: -1, Err: err}
	}

	result := &TaskResult{TaskName: name, ExitCode: -1}

	if err := cmd.Start(); err != nil {
		result.Err = err
		return result
	}

	// 取消/超时时终止整个进程组：CommandContext 只会杀 bash，rsync 等子进程可能残留。
	done := make(chan struct{})
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				terminateProcessGroup(cmd.Process)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					forceKillProcessGroup(cmd.Process)
				}
			case <-done:
			}
		}()
	}

	var (
		statsMu     sync.Mutex
		parsedStats *TaskStats
	)

	var wg sync.WaitGroup
	wg.Add(2)

	readLines := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			// 拦截 [STATS] 行，不传给 UI
			if s := parseStatsLine(line); s != nil {
				statsMu.Lock()
				parsedStats = s
				statsMu.Unlock()
				continue
			}
			if onLine != nil {
				onLine(line)
			}
		}
	}

	go readLines(stdout)
	go readLines(stderr)

	wg.Wait()

	waitErr := cmd.Wait()
	close(done)

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Err = waitErr
		}
	} else {
		result.ExitCode = 0
	}

	statsMu.Lock()
	result.Stats = parsedStats
	statsMu.Unlock()

	return result
}

// ---------- 路径工具 ----------

func expandTilde(p string) string {
	if p == "" {
		return p
	}
	if p == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

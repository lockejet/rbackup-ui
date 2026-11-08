package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAppLogDirWindows 验证 Windows 下的日志目录：%LOCALAPPDATA%\rbackup-ui\Logs
func TestAppLogDirWindows(t *testing.T) {
	base := `C:\Users\tester\AppData\Local`
	t.Setenv("LOCALAPPDATA", base)

	got := appLogDir("windows")
	want := filepath.Join(base, "rbackup-ui", "Logs")
	if got != want {
		t.Fatalf("appLogDir(windows) = %q, want %q", got, want)
	}
}

// TestAppLogDirWindowsNoLocalAppData 验证 LOCALAPPDATA 缺失时的回退：
// %USERPROFILE%\AppData\Local\rbackup-ui\Logs
func TestAppLogDirWindowsNoLocalAppData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := appLogDir("windows")
	want := filepath.Join(home, "AppData", "Local", "rbackup-ui", "Logs")
	if got != want {
		t.Fatalf("appLogDir(windows, no LOCALAPPDATA) = %q, want %q", got, want)
	}
}

// TestAppLogDirUnixXDG 验证非 Windows 下遵循 XDG_STATE_HOME。
func TestAppLogDirUnixXDG(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	got := appLogDir("linux")
	want := filepath.Join(state, "rbackup-ui", "log")
	if got != want {
		t.Fatalf("appLogDir(linux) = %q, want %q", got, want)
	}
}

// TestAppLogDirUnixFallback 验证 XDG_STATE_HOME 缺失/非绝对路径时回退到 ~/.local/state。
func TestAppLogDirUnixFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	for _, xdg := range []string{"", "relative/state"} {
		t.Setenv("XDG_STATE_HOME", xdg)
		got := appLogDir("linux")
		want := filepath.Join(home, ".local", "state", "rbackup-ui", "log")
		if got != want {
			t.Fatalf("appLogDir(linux, XDG_STATE_HOME=%q) = %q, want %q", xdg, got, want)
		}
	}
}

// TestDefaultLogDir 验证默认日志目录与 appLogDir 一致（TUI 与 rbackup-cli 的约定）。
func TestDefaultLogDir(t *testing.T) {
	got := defaultLogDir()
	if !strings.Contains(got, "rbackup-ui") {
		t.Fatalf("defaultLogDir() = %q, 应包含 rbackup-ui", got)
	}
	if got != appLogDir(runtime.GOOS) {
		t.Fatalf("defaultLogDir() = %q, want %q", got, appLogDir(runtime.GOOS))
	}
}

// TestResolveLogDirPrefersConfigured 验证可写时优先使用配置里的 LOG_DIR。
func TestResolveLogDirPrefersConfigured(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "configured")
	got := resolveLogDir(dir, "")
	if got != dir {
		t.Fatalf("resolveLogDir 可写时应使用配置目录，got %q want %q", got, dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("配置目录未被创建: %v", err)
	}
}

// TestResolveLogDirIgnoresEmptyConfig 验证 LOG_DIR 为空时不会把它当成有效目录。
func TestResolveLogDirIgnoresEmptyConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	got := resolveLogDir("", "")
	if got == "" {
		t.Fatal("resolveLogDir 不应返回空路径")
	}
	if !strings.Contains(got, "rbackup-ui") {
		t.Fatalf("resolveLogDir 应回退到应用日志目录，got %q", got)
	}
}

func TestToMSYSPath(t *testing.T) {
	cases := map[string]string{
		`C:\Users\admin\rbackup-cli`: "/c/Users/admin/rbackup-cli",
		`D:\a b\c.ini`:               "/d/a b/c.ini",
		`/already/posix`:             "/already/posix",
		`\\server\share\x`:           `\\server\share\x`,
		``:                           ``,
	}
	for in, want := range cases {
		if got := toMSYSPath(in); got != want {
			t.Fatalf("toMSYSPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsWSLLauncher(t *testing.T) {
	wsl := []string{
		`C:\Windows\System32\bash.exe`,
		`c:\windows\system32\bash.exe`,
		`C:\Windows\Sysnative\bash.exe`,
		`C:/Windows/System32/bash.exe`,
	}
	for _, p := range wsl {
		if !isWSLLauncher(p) {
			t.Fatalf("isWSLLauncher(%q) 应为 true", p)
		}
	}
	notWSL := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\msys64\usr\bin\bash.exe`,
		`D:\msys64\usr\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`/usr/bin/bash`,
		``,
	}
	for _, p := range notWSL {
		if isWSLLauncher(p) {
			t.Fatalf("isWSLLauncher(%q) 应为 false", p)
		}
	}
}

func TestParseCLIBashAndDoctor(t *testing.T) {
	oldMode, oldOpts := webMode, webOpts
	oldBash, oldDoctor := bashPathOverride, doctorMode
	defer func() {
		webMode, webOpts = oldMode, oldOpts
		bashPathOverride, doctorMode = oldBash, oldDoctor
	}()
	webMode = false
	webOpts = defaultWebOptions()
	bashPathOverride, doctorMode = "", false

	if _, _, err := parseCLI([]string{"rbackup-ui", "--bash", `C:\Program Files\Git\bin\bash.exe`}); err != nil {
		t.Fatalf("--bash 解析失败: %v", err)
	}
	if bashPathOverride != `C:\Program Files\Git\bin\bash.exe` {
		t.Fatalf("bashPathOverride = %q", bashPathOverride)
	}
	if _, _, err := parseCLI([]string{"rbackup-ui", "--doctor"}); err != nil || !doctorMode {
		t.Fatalf("--doctor 解析失败: err=%v doctor=%v", err, doctorMode)
	}
	if _, _, err := parseCLI([]string{"rbackup-ui", "--bash"}); err == nil {
		t.Fatal("--bash 缺值应报错")
	}
}

package main

// 运行环境诊断：`rbackup-tui --doctor`。
// 重点服务 Windows（MSYS2/Git Bash）用户排查 bash/rsync/ssh 与路径问题。

import (
	"fmt"
	"os"
	"runtime"
)

func orDash(s string) string {
	if s == "" {
		return "(未找到)"
	}
	return s
}

func runDoctorCLI() int {
	fmt.Printf("rbackup-ui %s  (%s/%s)\n\n", Version, runtime.GOOS, runtime.GOARCH)

	bash, bashErr := findBash()
	fmt.Println("依赖:")
	if bashErr != nil {
		fmt.Printf("  bash      ✗ %v\n", bashErr)
		if runtime.GOOS == "windows" {
			fmt.Println("            （请使用 Git for Windows / MSYS2 的 bash；C:\\Windows\\System32\\bash.exe 是 WSL 启动器，不能用）")
		}
	} else {
		fmt.Printf("  bash      ✓ %s\n", bash)
		for _, name := range []string{"rsync", "ssh", "sed", "awk"} {
			mark := "✓"
			if !probeCommand(bash, name) {
				mark = "✗"
			}
			fmt.Printf("  %-9s %s\n", name, mark)
		}
	}

	fmt.Println("\n路径:")
	script := findScriptPath()
	cfg := findConfigPath()
	fmt.Printf("  rbackup-cli : %s\n", orDash(script))
	fmt.Printf("  config.ini : %s\n", cfg)
	if _, err := os.Stat(cfg); err != nil {
		fmt.Printf("               （不存在，运行后可按样例创建）\n")
	}
	fmt.Printf("  日志目录   : %s\n", defaultLogDir())
	fmt.Printf("  Web 默认   : http://%s\n", defaultWebOptions().Addr)

	if runtime.GOOS == "windows" {
		fmt.Println("\nWindows 提示:")
		fmt.Println("  · bash 由 Git for Windows 或 MSYS2 提供；rsync 需 MSYS2 安装：pacman -S rsync")
		fmt.Println("  · 配置默认在 AppData\\Roaming\\rbackup-ui\\config.ini（也兼容 用户目录\\.config\\rbackup-ui\\）")
		fmt.Println("  · 日志默认在 AppData\\Local\\rbackup-ui\\Logs")
		fmt.Println("  · 局域网/手机访问需绑定 0.0.0.0 并放行 Windows 防火墙对应端口")
	}
	return 0
}

// warnRuntimeDeps 在启动时提示关键运行依赖缺失（不阻断界面启动）。
func warnRuntimeDeps() {
	if _, err := findBash(); err != nil {
		fmt.Fprintf(os.Stderr, "警告: %v\n", err)
		fmt.Fprintln(os.Stderr, "      备份任务将无法执行；界面本身仍可使用。")
		fmt.Fprintln(os.Stderr, "      可运行 rbackup-tui --doctor 查看环境诊断。")
	}
}

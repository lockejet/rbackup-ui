package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ---------- 版本信息（-ldflags 注入） ----------
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

var debugKeys = os.Getenv("RBACKUP_DEBUG_KEYS") != ""
var debugCount int

// ---------- 事件去重 ----------
var (
	dedupMu  sync.Mutex
	lastKey  tcell.Key
	lastRune rune
	lastMod  tcell.ModMask
	lastTime time.Time
)

func isDuplicateKey(event *tcell.EventKey) bool {
	dedupMu.Lock()
	defer dedupMu.Unlock()
	now := time.Now()
	dup := event.Key() == lastKey &&
		event.Rune() == lastRune &&
		event.Modifiers() == lastMod &&
		now.Sub(lastTime) < 50*time.Millisecond
	lastKey = event.Key()
	lastRune = event.Rune()
	lastMod = event.Modifiers()
	lastTime = now
	return dup
}

// ---------- 时长 / 字节 / 速率格式化（英文单位） ----------
func formatDuration(d time.Duration) string {
	totalSec := int(d.Seconds())
	if totalSec < 0 {
		totalSec = 0
	}
	if totalSec < 60 {
		return fmt.Sprintf("%ds", totalSec)
	}
	return fmt.Sprintf("%dmin%ds", totalSec/60, totalSec%60)
}

func formatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%dB", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.2fK", float64(b)/1024)
	}
	if b < 1024*1024*1024 {
		return fmt.Sprintf("%.2fM", float64(b)/(1024*1024))
	}
	return fmt.Sprintf("%.2fG", float64(b)/(1024*1024*1024))
}

func formatRate(bytes, ms int64) string {
	if ms <= 0 {
		return "0 KB/s"
	}
	return fmt.Sprintf("%.2f KB/s", float64(bytes)/1024/(float64(ms)/1000))
}

// k 生成按键提示，自动转义方括号，避免被 tview 当颜色标签吞掉
func k(s string) string {
	return tview.Escape("[" + s + "]")
}

// 将 $HOME 前缀替换为 ~
func shortenPath(p string) string {
	if p == "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// 挂载策略的中文描述
func policyDesc(policy string) string {
	switch policy {
	case "skip":
		return "门禁失败时跳过"
	case "fail":
		return "门禁失败时任务失败"
	case "ignore":
		return "不检查门禁"
	}
	return policy
}

// 焦点区中文名
func focusCN(area string) string {
	switch area {
	case "header":
		return "信息区"
	case "interact":
		return "交互区"
	default:
		return "任务区"
	}
}

// ---------- 交互区状态 ----------
type InteractState int

const (
	InteractIdle InteractState = iota
	InteractConfirming
	InteractRunning
	InteractDone
	InteractCancelled
)

func (s InteractState) String() string {
	switch s {
	case InteractIdle:
		return "空闲"
	case InteractConfirming:
		return "等待确认"
	case InteractRunning:
		return "同步中"
	case InteractDone:
		return "已完成"
	case InteractCancelled:
		return "已取消"
	}
	return "未知"
}

type ConfirmState struct {
	Queue        []*Task
	Index        int
	Decisions    map[string]bool
	NonDangerous []*Task
	DryRun       bool
}

type App struct {
	app   *tview.Application
	pages *tview.Pages

	configPath string
	scriptPath string
	cfg        *Config

	// UI 组件
	header    *tview.TextView
	tableArea *tview.Flex
	table     *tview.Table
	cmdView   *tview.TextView
	interact  *tview.Flex
	logPart   *tview.TextView

	statusLine1 *tview.TextView
	statusLine2 *tview.TextView
	statusLine3 *tview.TextView
	statusBar   *tview.Flex

	helpVisible bool
	helpPage    *tview.TextView

	// 运行状态
	running         bool
	cancel          context.CancelFunc
	cancelRequested bool

	focusArea string // "header" | "table" | "interact"

	// 运行摘要
	runCurrent     int
	runTotal       int
	runCurrentTask string
	runSuccess     int
	runSkipped     int
	runFailed      int
	runMountFailed int
	runStartTime   time.Time

	lastSuccess     int
	lastSkipped     int
	lastFailed      int
	lastMountFailed int
	lastRunDuration time.Duration

	// 临时状态消息（几秒后自动清除）
	tempStatus string
	statusGen  int

	confirmState  *ConfirmState
	interactState InteractState

	paused atomic.Bool

	logBufMu sync.Mutex
	logBuf   []string

	blinkStop chan struct{}
	blinkGen  int

	errMu         sync.Mutex
	currentErrors []string

	ctrlDCount int
	lastCtrlD  time.Time
}

// ---------- 路径查找 ----------

// ---------- 内嵌 CLI（单文件 bash 后端） ----------

// rbackup-cli 是单文件自包含的备份引擎（bash）。随二进制内嵌，首次运行时释放到
// <数据目录>/rbackup-ui/backend/，保证 UI 与引擎版本始终配套，且不受 PATH 中
// 旧版脚本影响；RBACKUP_SCRIPT 仍可覆盖（自定义/调试）。
//
//go:embed rbackup-cli
var embeddedCLI []byte

// xdgDataDir 返回数据目录（与安装器/脚本保持同一规则）。
func xdgDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" && filepath.IsAbs(d) {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

// backendDir 是内嵌 CLI 的释放目录。
func backendDir() string {
	return filepath.Join(xdgDataDir(), "rbackup-ui", "backend")
}

// extractEmbeddedCLI 把内嵌的 rbackup-cli 释放到 backendDir（内容变化时才重写），
// 返回释放后的路径。失败时返回错误，调用方继续尝试 PATH。
func extractEmbeddedCLI() (string, error) {
	dir := backendDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	cli := filepath.Join(dir, "rbackup-cli")
	want := string(embeddedCLI)
	cur, err := os.ReadFile(cli)
	if err != nil || string(cur) != want {
		tmp := cli + ".tmp"
		if err := os.WriteFile(tmp, embeddedCLI, 0o755); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, cli); err != nil {
			return "", err
		}
	}
	// VERSION：便于排查引擎与 UI 的版本是否配套
	if Version != "" && Version != "dev" {
		verFile := filepath.Join(dir, "VERSION")
		if cur, err := os.ReadFile(verFile); err != nil || strings.TrimSpace(string(cur)) != Version {
			_ = os.WriteFile(verFile, []byte(Version+"\n"), 0o644)
		}
	}
	return cli, nil
}

// detectMode 根据可执行文件名判断界面模式（硬链接/改名分发，BusyBox 风格）：
// rbackup-tui → tui；rbackup-web → web；其它名称默认 tui。
func detectMode(exeName string) string {
	base := strings.ToLower(filepath.Base(exeName))
	base = strings.TrimSuffix(base, ".exe")
	if strings.HasSuffix(base, "-web") {
		return "web"
	}
	return "tui"
}

// executableName 返回本次被调用的可执行文件名（/proc/self/exe 或 argv[0]）。
func executableName() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return os.Args[0]
}

// xdgConfigDir 返回用户配置根目录：Linux 为 $XDG_CONFIG_HOME 或 ~/.config，
// Windows 为 %AppData%，macOS 为 ~/Library/Application Support。
func xdgConfigDir() string {
	if d, err := os.UserConfigDir(); err == nil && d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// xdgStateDir 返回用户状态根目录（Linux 为 $XDG_STATE_HOME 或 ~/.local/state）
func xdgStateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state")
}

// appLogDir 返回应用日志目录，与 rbackup-cli 的 app_log_dir 保持一致，
// 否则 TUI 找不到 rbackup-cli 写出的 .stats：
//
//	Windows: %LOCALAPPDATA%\rbackup-ui\Logs
//	其他:    ${XDG_STATE_HOME:-~/.local/state}/rbackup-ui/log
func appLogDir(goos string) string {
	if goos == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "rbackup-ui", "Logs")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Local", "rbackup-ui", "Logs")
	}
	return filepath.Join(xdgStateDir(), "rbackup-ui", "log")
}

// legacyAppLogDir 是改名前的日志目录（rbackup-tui），仅用于兼容旧配置/旧 .stats。
func legacyAppLogDir(goos string) string {
	if goos == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "rbackup-tui", "Logs")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Local", "rbackup-tui", "Logs")
	}
	return filepath.Join(xdgStateDir(), "rbackup-tui", "log")
}

// defaultLogDir 返回当前平台的默认日志目录（config.ini 未写 LOG_DIR 时使用）。
func defaultLogDir() string {
	return appLogDir(runtime.GOOS)
}

// findScriptPath 解析备份引擎（rbackup-cli）路径，优先级：
//
//	RBACKUP_SCRIPT（显式指定）> 内嵌释放的副本 > PATH 中的 rbackup-cli
func findScriptPath() string {
	if p := os.Getenv("RBACKUP_SCRIPT"); p != "" {
		return p
	}
	if p, err := extractEmbeddedCLI(); err == nil {
		return p
	}
	if p, err := exec.LookPath("rbackup-cli"); err == nil {
		return p
	}
	return ""
}

// findConfigPath 按以下优先级解析配置文件：
//
//	-c/--config（在 NewApp 中覆盖）> RBACKUP_CONFIG
//	> $XDG_CONFIG_HOME/rbackup-ui/config.ini > $XDG_CONFIG_HOME/rbackup-tui/config.ini（改名兼容）
//	> ~/rbackup-ui/config.ini > ~/rbackup-tui/config.ini（改名兼容）> ~/rbackup/config.ini（更旧布局）
func findConfigPath() string {
	if p := os.Getenv("RBACKUP_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	xdgPath := filepath.Join(xdgConfigDir(), "rbackup-ui", "config.ini")
	candidates := []string{
		xdgPath,
		filepath.Join(xdgConfigDir(), "rbackup-tui", "config.ini"), // 改名兼容
		filepath.Join(home, ".config", "rbackup-ui", "config.ini"), // Git Bash/MSYS2 安装器布局
		filepath.Join(home, "rbackup-ui", "config.ini"),
		filepath.Join(home, "rbackup-tui", "config.ini"), // 改名兼容
		filepath.Join(home, "rbackup", "config.ini"),     // 更旧布局
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return xdgPath
}

// Web 模式开关与选项（--web 相关参数；默认值见 web.go）。
var (
	webMode    = detectMode(executableName()) == "web"
	webOpts    = defaultWebOptions()
	doctorMode bool
)

// argValue 取形如 `--flag value` 的值；缺失或下一个参数是另一个选项时报错。
func argValue(args []string, i *int, flag string) (string, error) {
	if *i+1 >= len(args) {
		return "", fmt.Errorf("%s 缺少参数值", flag)
	}
	next := args[*i+1]
	// "-" 是常见约定（如 --web-audit-log - 表示 stderr），其余以 - 开头视为漏值
	if next != "-" && strings.HasPrefix(next, "-") {
		return "", fmt.Errorf("%s 缺少参数值", flag)
	}
	*i++
	return args[*i], nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `rbackup-tui / rbackup-web %s —— rbackup-cli 的终端界面（TUI）/ Web 界面

用法:
  rbackup-tui [全局选项]              启动 TUI（默认）
  rbackup-web [Web 选项]              启动 Web 界面（不启动 TUI）
  rbackup-tui --web [Web 选项]        同上（--web 兼容写法）

说明:
  只要命令行里出现任一 --web-* 选项，就会自动启用 Web 模式（--web 可省略）。

全局选项:
  -c, --config <文件>     配置文件路径
  -s, --script <文件>     rbackup-cli 路径
  --bash <文件>           指定 bash（默认自动探测；Windows 可指向 Git/MSYS2 的 bash.exe）
  --doctor                打印运行环境诊断（bash/rsync/ssh/路径）后退出
  -h, --help              显示本帮助

Web 选项:
  --web                         启用 Web 模式（可省略）
  --web-addr <地址>             监听地址（默认 127.0.0.1:30031）
  --web-token-file <文件>       token 文件（推荐，避免出现在 ps 中）
  --web-token <值>              直接指定 token（不推荐）
  --web-tls-cert <文件>         TLS 证书（非 loopback 监听时必需）
  --web-tls-key <文件>          TLS 私钥
  --web-insecure-http           允许"非 loopback + 明文 HTTP"（仅限隔离内网/VPN）
  --web-audit-log <文件>        审计日志（默认 <日志目录>/web-audit.log，"-" 只写 stderr）
  --web-max-clients <n>         SSE 客户端上限（默认 8）
  --web-read-only               只读模式：禁止触发任务，只观察
  --web-trust-proxy             信任反向代理头（X-Forwarded-Proto/Host/For；仅反代后启用）
  --web-public-url <URL>        对外访问地址（如 https://backup.example.com；用于扫码链接/同源校验）

登录/二次验证（可选）:
  --web-user <名称>             用户名（配合密码登录，默认 admin）
  --web-password-hash <哈希>    密码哈希（不推荐直接写在命令行）
  --web-password-hash-file <f>  密码哈希文件（推荐；先跑 --web-hash-password 生成）
  --web-2fa <off|totp>          2FA 策略（默认 off；totp 需配合密码登录，认证器在 Web 安全页绑定）
  --web-totp-secret-file <f>    TOTP secret 文件（可省略：在 Web 安全页扫码绑定后自动生成）
  --web-hash-password           交互式生成密码哈希后退出
  --web-gen-totp                生成 TOTP secret 与 otpauth URI（终端里显示二维码）
  --web-show-totp-qr            显示"现有" TOTP secret 的二维码（不轮换）
  --web-show-totp-code          用现有 secret 计算当前动态码（仅排查用）
  --web-check-totp-code <码>    校验一个动态码是否与 secret/时钟匹配（仅排查用）

Web 界面内置：安全中心（绑定 2FA / 重新生成备份码 / 关闭 2FA）、手机扫码登录、
中英文切换、亮/暗主题切换；未启用 2FA 时登录后会提示一键开启。

环境变量: RBACKUP_WEB_TOKEN / RBACKUP_WEB_TOKEN_FILE / RBACKUP_WEB_ADDR /
          RBACKUP_WEB_TRUST_PROXY（1/true/yes/on）/ RBACKUP_WEB_PUBLIC_URL
安全默认: 监听非 loopback 地址必须 token + TLS，否则拒绝启动；
          仅当显式 --web-insecure-http 时才允许明文（内网/VPN 场景）。
          反向代理场景请加 --web-trust-proxy，并在 HTTPS 终止的代理上传递
          X-Forwarded-Proto/Host/For。
`, Version)
}

// parseCLI 解析命令行与环境变量，返回配置/脚本路径；web 模式相关状态写入包级变量。
func parseCLI(args []string) (configPath, scriptPath string, err error) {
	configPath = findConfigPath()
	scriptPath = findScriptPath()

	// 环境变量先于命令行参数（命令行可覆盖）
	if v := os.Getenv("RBACKUP_WEB_TOKEN"); v != "" {
		webOpts.Token = v
	}
	if v := os.Getenv("RBACKUP_WEB_TOKEN_FILE"); v != "" {
		webOpts.TokenFile = v
	}
	if v := os.Getenv("RBACKUP_WEB_ADDR"); v != "" {
		webOpts.Addr = v
	}
	if v := os.Getenv("RBACKUP_WEB_USER"); v != "" {
		webOpts.User = v
	}
	if v := os.Getenv("RBACKUP_WEB_PASSWORD_HASH"); v != "" {
		webOpts.PasswordHash = v
	}
	if v := os.Getenv("RBACKUP_WEB_PASSWORD_HASH_FILE"); v != "" {
		webOpts.PasswordHashFile = v
	}
	if v := os.Getenv("RBACKUP_WEB_2FA"); v != "" {
		webOpts.TwoFA = v
	}
	if v := os.Getenv("RBACKUP_WEB_TOTP_SECRET_FILE"); v != "" {
		webOpts.TOTPSecretFile = v
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("RBACKUP_WEB_TRUST_PROXY"))); v != "" {
		if v == "1" || v == "true" || v == "yes" || v == "on" {
			webOpts.TrustProxy = true
		}
	}
	if v := os.Getenv("RBACKUP_WEB_PUBLIC_URL"); v != "" {
		webOpts.PublicURL = v
	}

	for i := 1; i < len(args); i++ {
		var v string
		switch args[i] {
		case "-c", "--config":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			configPath = expandTilde(v)
		case "-s", "--script":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			scriptPath = expandTilde(v)
		case "-h", "--help":
			usage()
			os.Exit(0)
		case "--web":
			webMode = true
		case "--web-addr":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.Addr = v
		case "--web-token":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.Token = v
		case "--web-token-file":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.TokenFile = v
		case "--web-tls-cert":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.TLSCert = v
		case "--web-tls-key":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.TLSKey = v
		case "--web-audit-log":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.AuditLog = v
		case "--web-insecure-http":
			webMode = true
			webOpts.InsecureHTTP = true
		case "--bash":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			bashPathOverride = v
		case "--doctor":
			doctorMode = true
		case "--web-read-only":
			webMode = true
			webOpts.ReadOnly = true
		case "--web-trust-proxy":
			webMode = true
			webOpts.TrustProxy = true
		case "--web-public-url":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.PublicURL = v
		case "--web-user":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.User = v
		case "--web-password-hash":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.PasswordHash = v
		case "--web-password-hash-file":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.PasswordHashFile = v
		case "--web-2fa":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.TwoFA = v
		case "--web-totp-secret-file":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			webOpts.TOTPSecretFile = v
		case "--web-hash-password":
			os.Exit(runHashPasswordCLI())
		case "--web-gen-totp":
			os.Exit(runGenTOTPCLI(webOpts.User))
		case "--web-show-totp-qr":
			os.Exit(runShowTOTPCLI(webOpts.TOTPSecretFile, webOpts.User))
		case "--web-show-totp-code":
			os.Exit(runShowTOTPCodeCLI(webOpts.TOTPSecretFile))
		case "--web-check-totp-code":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			os.Exit(runCheckTOTPCodeCLI(webOpts.TOTPSecretFile, v))
		case "--web-max-clients":
			if v, err = argValue(args, &i, args[i]); err != nil {
				return "", "", err
			}
			webMode = true
			if n, e := strconv.Atoi(v); e == nil && n > 0 {
				webOpts.MaxClients = n
			} else {
				return "", "", fmt.Errorf("--web-max-clients 需要正整数，得到 %q", v)
			}
		}
	}

	if configPath != "" {
		if abs, e := filepath.Abs(configPath); e == nil {
			configPath = abs
		}
	}
	if scriptPath != "" {
		if abs, e := filepath.Abs(scriptPath); e == nil {
			scriptPath = abs
		}
	}
	return configPath, scriptPath, nil
}

func NewApp() *App {
	configPath, scriptPath, err := parseCLI(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "参数错误: %v\n\n", err)
		usage()
		os.Exit(2)
	}

	return &App{
		app:           tview.NewApplication(),
		configPath:    configPath,
		scriptPath:    scriptPath,
		focusArea:     "table",
		interactState: InteractIdle,
	}
}

// ---------- 日志目录回退检测 ----------
func dirWritable(dir string) bool {
	if dir == "" {
		return false
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false
	}
	test := filepath.Join(dir, fmt.Sprintf(".rbackup_test_%d", os.Getpid()))
	f, err := os.Create(test)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(test)
	return true
}

// resolveLogDir 与 rbackup-cli 的 LOG_DIR 回退链保持一致，否则 TUI 会找不到 .stats：
//
//	配置里的 LOG_DIR → 平台默认目录（Windows %LOCALAPPDATA%\rbackup-ui\Logs，
//	其他 $XDG_STATE_HOME/rbackup-ui/log）→ <脚本同目录>/log（旧行为，最后兜底）
func resolveLogDir(cfgLogDir, scriptPath string) string {
	if dirWritable(cfgLogDir) {
		return cfgLogDir
	}
	stateFallback := appLogDir(runtime.GOOS)
	if dirWritable(stateFallback) {
		return stateFallback
	}
	// 改名兼容：旧版本 rbackup-cli 写的是 rbackup-tui/log，仍能读到旧 .stats
	if legacy := legacyAppLogDir(runtime.GOOS); dirWritable(legacy) {
		return legacy
	}
	if scriptPath != "" {
		fallback := filepath.Join(filepath.Dir(scriptPath), "log")
		if dirWritable(fallback) {
			return fallback
		}
	}
	return cfgLogDir
}

// ---------- UI ----------
func (a *App) setupUI() {
	a.header = tview.NewTextView().SetDynamicColors(true).
		SetScrollable(true).SetWrap(false).SetWordWrap(false)
	a.header.SetBorder(true).SetTitle(" rbackup [1] ")

	a.table = tview.NewTable().SetSelectable(true, false).SetBorders(false)
	a.table.SetFixed(1, 0)
	a.table.SetBorder(false)

	a.cmdView = tview.NewTextView().SetDynamicColors(true).
		SetScrollable(true).SetWrap(false).SetWordWrap(false)

	a.tableArea = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.table, 0, 1, true).
		AddItem(a.cmdView, 2, 0, false)
	a.tableArea.SetBorder(true).SetTitle(" [2] 任务列表 ")

	a.logPart = tview.NewTextView().SetDynamicColors(true).
		SetScrollable(true).SetWrap(false).SetWordWrap(false)

	a.interact = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.logPart, 0, 1, false)
	a.interact.SetBorder(true).SetTitle(" [3] 交互区 ")

	a.statusLine1 = tview.NewTextView().SetDynamicColors(true)
	a.statusLine2 = tview.NewTextView().SetDynamicColors(true)
	a.statusLine3 = tview.NewTextView().SetDynamicColors(true)
	a.statusLine3.SetText(
		"[yellow]全局:[-] 切换" + k("Tab") + " 直选" + k("1/2/3") +
			" 停止" + k("Ctrl+C") + " 强退" + k("Ctrl+D×3") +
			" 退出" + k("q") + " 帮助" + k("F1/?"))
	a.statusBar = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.statusLine1, 1, 0, false).
		AddItem(a.statusLine2, 1, 0, false).
		AddItem(a.statusLine3, 1, 0, false)

	a.table.SetSelectionChangedFunc(func(row, col int) {
		a.updateCmdView()
	})

	a.app.SetInputCapture(a.globalInputCapture)

	a.pages = tview.NewPages()
	a.pages.AddPage("main", a.mainLayout(), true, true)
}

func (a *App) mainLayout() tview.Primitive {
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 4, 0, false).
		AddItem(a.tableArea, 0, 3, true).
		AddItem(a.interact, 0, 2, false).
		AddItem(a.statusBar, 3, 0, false)
}

// ---------- 焦点 ----------
func (a *App) setFocus(area string) {
	a.focusArea = area
	switch area {
	case "header":
		a.app.SetFocus(a.header)
	case "interact":
		a.app.SetFocus(a.logPart)
	default:
		a.app.SetFocus(a.table)
	}
	a.updateFocusStyle()
	a.updateStatusBar()
}

func (a *App) toggleFocus() {
	switch a.focusArea {
	case "header":
		a.setFocus("table")
	case "table":
		a.setFocus("interact")
	default:
		a.setFocus("header")
	}
}

func (a *App) updateFocusStyle() {
	headerColor := tcell.ColorGray
	tableColor := tcell.ColorGray
	interactColor := tcell.ColorGray

	switch a.focusArea {
	case "header":
		headerColor = tcell.ColorGreen
	case "table":
		tableColor = tcell.ColorGreen
	case "interact":
		interactColor = tcell.ColorGreen
	}
	a.header.SetBorderColor(headerColor)
	a.tableArea.SetBorderColor(tableColor)
	a.interact.SetBorderColor(interactColor)

	a.updateCmdView()
}

// ---------- 状态栏 ----------
func (a *App) updateStatusBar() {
	a.updateStatusLine1()
	a.updateStatusLine2()
}

func (a *App) updateStatusLine1() {
	if a.tempStatus != "" {
		return
	}
	if a.confirmState != nil {
		return
	}
	if a.running {
		if a.paused.Load() {
			a.statusLine1.SetText("[yellow]⏸ 已暂停（按 p 继续）[-]")
			return
		}
		a.statusLine1.SetText(fmt.Sprintf(
			"[green]运行中[-]   进度 %d/%d   当前 [yellow]%s[-]   "+
				"[green]成功 %d[-]  [yellow]跳过 %d[-]  [red]失败 %d[-]  "+
				"[orange]挂载门禁失败 %d[-]   用时 %s",
			a.runCurrent, a.runTotal, a.runCurrentTask,
			a.runSuccess, a.runSkipped, a.runFailed, a.runMountFailed,
			formatDuration(time.Since(a.runStartTime))))
		return
	}
	if a.interactState == InteractDone || a.interactState == InteractCancelled {
		label := "[green]运行完成[-]"
		if a.interactState == InteractCancelled {
			label = "[yellow]运行被取消[-]"
		}
		a.statusLine1.SetText(fmt.Sprintf(
			"%s   [green]成功 %d[-]  [yellow]跳过 %d[-]  [red]失败 %d[-]  "+
				"[orange]挂载门禁失败 %d[-]   总用时 %s",
			label, a.lastSuccess, a.lastSkipped, a.lastFailed, a.lastMountFailed,
			formatDuration(a.lastRunDuration)))
		return
	}
	sel := 0
	for _, t := range a.cfg.Tasks {
		if t.Selected {
			sel++
		}
	}
	a.statusLine1.SetText(fmt.Sprintf(
		"[green]就绪[-]   已选: %d/%d   焦点: [yellow]%s[-]",
		sel, len(a.cfg.Tasks), focusCN(a.focusArea)))
}

func (a *App) updateStatusLine2() {
	if a.confirmState != nil {
		a.statusLine2.SetText(
			"[yellow]确认:[-] y 确认 n 跳过 a 全部确认 s 全部跳过    " +
				"[yellow]滚动:[-] " + k("↑↓/jk") + " " + k("PgUp/PgDn") +
				" " + k("←→/hl") + " " + k("0/$"))
		return
	}
	switch a.focusArea {
	case "header":
		a.statusLine2.SetText(
			"[yellow]滚动:[-] 横滚" + k("←→/hl") + " 滚动" + k("↑↓/jk") +
				" 翻页" + k("PgUp/PgDn") + " 纵首尾" + k("g/G") +
				" 横首尾" + k("0/$"))
	case "interact":
		a.statusLine2.SetText(
			"[yellow]滚动:[-] 滚动" + k("↑↓/jk") + " 横滚" + k("←→/hl") +
				" 翻页" + k("PgUp/PgDn") + " 纵首尾" + k("g/G") +
				" 横首尾" + k("0/$") + " 暂停" + k("p/空格"))
	default:
		a.statusLine2.SetText(
			"[yellow]滚动:[-] 移动" + k("↑↓/jk") + " 横滚" + k("←→/hl") +
				" 翻页" + k("PgUp/PgDn") + " 纵首尾" + k("g/G") +
				" 横首尾" + k("0/$") + "    " +
				"[yellow]选择:[-] 勾选" + k("空格") + " 全选" + k("a") +
				" 清空" + k("n") + "    " +
				"[yellow]执行:[-] 运行" + k("Enter") + " 预览" + k("d") +
				" 挂载检查" + k("m") + " 刷新" + k("r"))
	}
}

func (a *App) setStatus(msg string) {
	a.tempStatus = msg
	a.statusGen++
	gen := a.statusGen
	a.statusLine1.SetText(msg)
	go func() {
		time.Sleep(2 * time.Second)
		a.app.QueueUpdateDraw(func() {
			if a.statusGen == gen {
				a.tempStatus = ""
				a.updateStatusLine1()
			}
		})
	}()
}

// ---------- 闪烁（状态栏第 1 行） ----------
func (a *App) startStatusBlink(text string) {
	if a.blinkStop != nil {
		close(a.blinkStop)
		a.blinkStop = nil
	}
	a.blinkGen++
	gen := a.blinkGen

	stop := make(chan struct{})
	a.blinkStop = stop

	render := func(on bool) {
		a.app.QueueUpdateDraw(func() {
			if a.blinkGen != gen {
				return
			}
			if on {
				a.statusLine1.SetText("[red::b]" + text + "[-:-:-]")
			} else {
				a.statusLine1.SetText("[yellow::b]" + text + "[-:-:-]")
			}
		})
	}

	// 首次渲染异步，避免阻塞输入处理器
	go render(true)

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		on := false
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				on = !on
				render(on)
			}
		}
	}()
}

func (a *App) stopStatusBlink() {
	if a.blinkStop != nil {
		close(a.blinkStop)
		a.blinkStop = nil
	}
	a.blinkGen++
	a.updateStatusLine1()
}

// ---------- Header ----------
func (a *App) updateHeader() {
	logDir := resolveLogDir(a.cfg.Global.LogDir, a.scriptPath)
	base := "rbackup_" + time.Now().Format("20060102_1504")
	logPath := filepath.Join(logDir, base+".log")
	statsPath := filepath.Join(logDir, base+".stats")

	sshUser := a.cfg.Global.SSHUser
	if sshUser == "" {
		sshUser = "admin"
	}
	policy := a.cfg.Global.MountPolicy

	txt := fmt.Sprintf(
		"[yellow]脚本:[-] %s  [yellow]配置:[-] %s  [yellow]策略:[-] %s（%s）\n"+
			"[yellow]远端:[-] %s@%s:%s  [yellow]日志:[-] %s  [yellow]统计:[-] %s",
		shortenPath(a.scriptPath),
		shortenPath(a.configPath),
		policy, policyDesc(policy),
		sshUser, a.cfg.Global.Host, a.cfg.Global.SSHPort,
		shortenPath(logPath),
		shortenPath(statsPath))
	a.header.SetText(txt)
}

// ---------- 表格 ----------
func headerCell(text string) *tview.TableCell {
	return tview.NewTableCell(text).
		SetTextColor(tcell.ColorYellow).
		SetSelectable(false).
		SetAlign(tview.AlignLeft).
		SetExpansion(0)
}

func (a *App) refreshTasks() {
	a.table.Clear()

	headers := []string{"", "任务名", "源", "目标", "门禁"}
	for i, h := range headers {
		a.table.SetCell(0, i, headerCell(h))
	}

	var currentConfirmName string
	if a.confirmState != nil && a.confirmState.Index < len(a.confirmState.Queue) {
		currentConfirmName = a.confirmState.Queue[a.confirmState.Index].Name
	}

	for i, task := range a.cfg.Tasks {
		row := i + 1
		highlight := currentConfirmName != "" && task.Name == currentConfirmName
		bg := tcell.ColorDarkRed
		fg := tcell.ColorWhite

		check := "○"
		if task.Selected {
			check = "●"
		}
		checkCell := tview.NewTableCell(check).
			SetAlign(tview.AlignCenter).SetExpansion(0)
		if task.Selected {
			checkCell.SetTextColor(tcell.ColorGreen)
		}
		if highlight {
			checkCell.SetBackgroundColor(bg).SetTextColor(fg)
		}
		a.table.SetCell(row, 0, checkCell)

		nameCell := tview.NewTableCell(task.Name).SetExpansion(0)
		if task.IsDangerous(a.cfg.Global) {
			nameCell.SetTextColor(tcell.ColorOrange)
		}
		if highlight {
			nameCell.SetBackgroundColor(bg).SetTextColor(fg)
		}
		a.table.SetCell(row, 1, nameCell)

		srcCell := tview.NewTableCell(task.Src).SetExpansion(1)
		if highlight {
			srcCell.SetBackgroundColor(bg).SetTextColor(fg)
		}
		a.table.SetCell(row, 2, srcCell)

		dstCell := tview.NewTableCell(task.Dst).SetExpansion(1)
		if highlight {
			dstCell.SetBackgroundColor(bg).SetTextColor(fg)
		}
		a.table.SetCell(row, 3, dstCell)

		gateTxt := "—"
		gateColor := tcell.ColorGray
		switch task.MountGateMode() {
		case "require_mounted":
			gateTxt = "已挂载"
			gateColor = tcell.ColorLightBlue
		case "require_unmounted":
			gateTxt = "未挂载"
			gateColor = tcell.ColorLightBlue
		}
		gateCell := tview.NewTableCell(gateTxt).
			SetTextColor(gateColor).SetExpansion(0)
		if highlight {
			gateCell.SetBackgroundColor(bg).SetTextColor(fg)
		}
		a.table.SetCell(row, 4, gateCell)
	}

	if len(a.cfg.Tasks) > 0 {
		row, _ := a.table.GetSelection()
		if row < 1 || row > len(a.cfg.Tasks) {
			row = 1
		}
		a.table.Select(row, 0)
	}
	a.updateCmdView()
}

// ---------- 命令区 ----------
func (a *App) buildRsyncCommand(t *Task) string {
	if t == nil {
		return ""
	}
	g := a.cfg.Global
	parts := []string{"rsync"}

	if g.GlobalOpts != "" {
		parts = append(parts, g.GlobalOpts)
	}
	if t.Opts != "" {
		parts = append(parts, t.Opts)
	}

	if t.NeedsDelete() {
		dst := strings.TrimRight(t.Dst, "/")
		recycle := dst + "/.deleted_files/rbackup/<ts>"
		parts = append(parts,
			"--delete",
			"--exclude='/.deleted_files/'",
			"--backup",
			fmt.Sprintf("--backup-dir=\"%s\"", recycle))
	}

	if t.NeedsRemoveSource(g) {
		parts = append(parts, "--remove-source-files")
	}

	sshCmd := fmt.Sprintf("ssh -p %s -i %s", g.SSHPort, g.SSHKey)
	parts = append(parts, "-e", fmt.Sprintf("\"%s\"", sshCmd))
	parts = append(parts, fmt.Sprintf("\"%s\"", t.Src))

	sshUser := g.SSHUser
	if sshUser == "" {
		sshUser = "admin"
	}
	fullDst := t.Dst
	if !strings.Contains(fullDst, "@") {
		fullDst = fmt.Sprintf("%s@%s:%s", sshUser, g.Host, fullDst)
	}
	parts = append(parts, fmt.Sprintf("\"%s\"", fullDst))

	return strings.Join(parts, " ")
}

func (a *App) updateCmdView() {
	row, _ := a.table.GetSelection()
	var cmd string
	if row > 0 && row <= len(a.cfg.Tasks) {
		cmd = a.buildRsyncCommand(a.cfg.Tasks[row-1])
	}
	if cmd == "" {
		cmd = "(未选择任务)"
	}
	focused := a.focusArea == "table"
	if focused {
		a.cmdView.SetText("[yellow]> 命令:[-] [white]" + tview.Escape(cmd) + "[-]")
	} else {
		a.cmdView.SetText("[darkgray]> 命令: " + tview.Escape(cmd) + "[-]")
	}
}

// ---------- 横向滚动辅助 ----------
func (a *App) scrollTextHorizontal(tv *tview.TextView, delta int) bool {
	row, col := tv.GetScrollOffset()
	col += delta
	if col < 0 {
		col = 0
	}
	tv.ScrollTo(row, col)
	return true
}

// 命令区横滚（表格不横滚）
func (a *App) scrollCmdViewHorizontal(delta int) {
	_, col := a.cmdView.GetScrollOffset()
	col += delta
	if col < 0 {
		col = 0
	}
	a.cmdView.ScrollTo(0, col)
}

// ---------- 日志滚动 ----------
func (a *App) scrollLogVertical(delta int) {
	row, col := a.logPart.GetScrollOffset()
	row += delta
	if row < 0 {
		row = 0
	}
	a.logPart.ScrollTo(row, col)
}

func (a *App) scrollLogHorizontal(delta int) bool {
	return a.scrollTextHorizontal(a.logPart, delta)
}

func (a *App) flushLogBuf() {
	a.logBufMu.Lock()
	buf := a.logBuf
	a.logBuf = nil
	a.logBufMu.Unlock()
	for _, line := range buf {
		fmt.Fprintln(a.logPart, tview.Escape(line))
	}
}

func (a *App) handleLogScroll(event *tcell.EventKey) bool {
	scrolled := false
	upward := false
	toBottom := false
	horizontal := false

	switch event.Key() {
	case tcell.KeyUp:
		a.scrollLogVertical(-1)
		scrolled, upward = true, true
	case tcell.KeyDown:
		a.scrollLogVertical(1)
		scrolled = true
	case tcell.KeyLeft:
		a.scrollLogHorizontal(-1)
		scrolled, horizontal = true, true
	case tcell.KeyRight:
		a.scrollLogHorizontal(1)
		scrolled, horizontal = true, true
	case tcell.KeyPgUp:
		a.scrollLogVertical(-10)
		scrolled, upward = true, true
	case tcell.KeyPgDn:
		a.scrollLogVertical(10)
		scrolled = true
	case tcell.KeyHome:
		_, col := a.logPart.GetScrollOffset()
		a.logPart.ScrollTo(0, col)
		scrolled, upward = true, true
	case tcell.KeyEnd:
		_, col := a.logPart.GetScrollOffset()
		a.logPart.ScrollTo(1<<30, col)
		scrolled, toBottom = true, true
	}
	if !scrolled && event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'j':
			a.scrollLogVertical(1)
			scrolled = true
		case 'k':
			a.scrollLogVertical(-1)
			scrolled, upward = true, true
		case 'h':
			a.scrollLogHorizontal(-1)
			scrolled, horizontal = true, true
		case 'l':
			a.scrollLogHorizontal(1)
			scrolled, horizontal = true, true
		case 'g':
			_, col := a.logPart.GetScrollOffset()
			a.logPart.ScrollTo(0, col)
			scrolled, upward = true, true
		case 'G':
			_, col := a.logPart.GetScrollOffset()
			a.logPart.ScrollTo(1<<30, col)
			scrolled, toBottom = true, true
		case '0':
			row, _ := a.logPart.GetScrollOffset()
			a.logPart.ScrollTo(row, 0) // 横向：行首
			scrolled, horizontal = true, true
		case '$':
			row, _ := a.logPart.GetScrollOffset()
			a.logPart.ScrollTo(row, 1<<30) // 横向：行尾
			scrolled, horizontal = true, true
		}
	}
	if !scrolled {
		return false
	}

	if a.running && !horizontal {
		if upward && !a.paused.Load() {
			a.paused.Store(true)
			a.updateStatusLine1()
		} else if toBottom && a.paused.Load() {
			a.paused.Store(false)
			a.flushLogBuf()
			a.updateStatusLine1()
		}
	}
	return true
}

// ---------- 表格导航 ----------
func (a *App) handleTableNav(event *tcell.EventKey) bool {
	row, col := a.table.GetSelection()
	maxRow := len(a.cfg.Tasks)
	if maxRow == 0 {
		return false
	}
	moveTo := func(r int) {
		if r < 1 {
			r = 1
		}
		if r > maxRow {
			r = maxRow
		}
		a.table.Select(r, col)
	}
	switch event.Key() {
	case tcell.KeyUp:
		moveTo(row - 1)
		return true
	case tcell.KeyDown:
		moveTo(row + 1)
		return true
	case tcell.KeyPgUp:
		moveTo(row - 10)
		return true
	case tcell.KeyPgDn:
		moveTo(row + 10)
		return true
	case tcell.KeyHome:
		moveTo(1)
		return true
	case tcell.KeyEnd:
		moveTo(maxRow)
		return true
	}
	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'j':
			moveTo(row + 1)
			return true
		case 'k':
			moveTo(row - 1)
			return true
		case 'g':
			moveTo(1)
			return true
		case 'G':
			moveTo(maxRow)
			return true
		}
	}
	return false
}

// ---------- 请求取消 ----------
func (a *App) requestCancel() {
	if a.cancelRequested || a.cancel == nil {
		return
	}
	a.cancel()
	a.cancel = nil
	a.cancelRequested = true
	a.updateStatusLine1()
	a.updateStatusLine2()
}

// ---------- Ctrl+D 提示 ----------
func (a *App) showCtrlDPrompt(remaining int) {
	a.setStatus(fmt.Sprintf("[yellow]Ctrl+D 再按 %d 次退出程序[-]", remaining))
}

// ---------- 全局按键 ----------
func (a *App) globalInputCapture(event *tcell.EventKey) *tcell.EventKey {
	if isDuplicateKey(event) {
		return nil
	}
	if debugKeys {
		debugCount++
		if !(event.Key() == 64 && event.Modifiers() == 2) {
			fmt.Fprintf(os.Stderr, "[KEY#%d][global] Key=%v Rune=%q Mod=%v\n",
				debugCount, event.Key(), event.Rune(), event.Modifiers())
		}
	}

	// F1 / ? 帮助
	if event.Key() == tcell.KeyF1 {
		if a.helpVisible {
			a.hideHelp()
		} else {
			a.showHelp()
		}
		return nil
	}
	if event.Key() == tcell.KeyRune && event.Rune() == '?' {
		if a.helpVisible {
			a.hideHelp()
		} else {
			a.showHelp()
		}
		return nil
	}

	// 帮助浮层内的按键
	if a.helpVisible {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlC:
			a.hideHelp()
			return nil
		}
		if event.Key() == tcell.KeyRune {
			switch event.Rune() {
			case 'q', 'Q':
				a.hideHelp()
				return nil
			}
		}
		return event
	}

	// Ctrl+D ×3 强制退出
	if event.Key() == tcell.KeyCtrlD {
		now := time.Now()
		if now.Sub(a.lastCtrlD) < 1500*time.Millisecond {
			a.ctrlDCount++
		} else {
			a.ctrlDCount = 1
		}
		a.lastCtrlD = now
		if a.ctrlDCount >= 3 {
			a.app.Stop()
			return nil
		}
		a.showCtrlDPrompt(3 - a.ctrlDCount)
		return nil
	}
	if a.ctrlDCount > 0 {
		a.ctrlDCount = 0
	}

	// 焦点切换
	if event.Key() == tcell.KeyTab {
		a.toggleFocus()
		return nil
	}
	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case '1':
			a.setFocus("header")
			return nil
		case '2':
			a.setFocus("table")
			return nil
		case '3':
			a.setFocus("interact")
			return nil
		}
	}

	if a.confirmState != nil {
		return a.handleConfirmKey(event)
	}
	if a.running {
		return a.handleRunningKey(event)
	}
	return a.handleIdleKey(event)
}

// ---------- 危险确认 ----------
func (a *App) handleConfirmKey(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		a.confirmAllSkip()
		return nil
	}
	if event.Key() == tcell.KeyEscape {
		return nil
	}
	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'q', 'Q':
			return nil
		}
	}
	a.handleLogScroll(event)

	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'y', 'Y':
			a.confirmCurrent(true)
			return nil
		case 'n', 'N':
			a.confirmCurrent(false)
			return nil
		case 'a', 'A':
			a.confirmAllConfirm()
			return nil
		case 's', 'S':
			a.confirmAllSkip()
			return nil
		}
	}
	return nil
}

// ---------- 运行中 ----------
func (a *App) handleRunningKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyCtrlC:
		a.requestCancel()
		return nil
	case tcell.KeyEscape:
		return nil
	}
	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'p', 'P', ' ':
			a.togglePause()
			return nil
		case 'q', 'Q':
			return nil
		}
	}
	if a.handleLogScroll(event) {
		return nil
	}
	if a.focusArea == "header" {
		a.handleHeaderScroll(event)
	}
	return nil
}

// ---------- 空闲 ----------
func (a *App) handleIdleKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		if a.focusArea == "interact" {
			a.setFocus("table")
			return nil
		}
		a.app.Stop()
		return nil
	case tcell.KeyCtrlC:
		a.app.Stop()
		return nil
	}
	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'q', 'Q':
			a.app.Stop()
			return nil
		}
	}
	switch a.focusArea {
	case "interact":
		return a.handleInteractIdleKey(event)
	case "header":
		return a.handleHeaderIdleKey(event)
	default:
		return a.handleTableIdleKey(event)
	}
}

func (a *App) handleHeaderIdleKey(event *tcell.EventKey) *tcell.EventKey {
	a.handleHeaderScroll(event)
	return nil
}

func (a *App) handleHeaderScroll(event *tcell.EventKey) bool {
	handled := false
	switch event.Key() {
	case tcell.KeyLeft:
		a.scrollTextHorizontal(a.header, -1)
		handled = true
	case tcell.KeyRight:
		a.scrollTextHorizontal(a.header, 1)
		handled = true
	case tcell.KeyUp:
		a.scrollTextHorizontal(a.header, 0)
		handled = true
	case tcell.KeyDown:
		a.scrollTextHorizontal(a.header, 0)
		handled = true
	case tcell.KeyPgUp:
		a.scrollTextHorizontal(a.header, -10)
		handled = true
	case tcell.KeyPgDn:
		a.scrollTextHorizontal(a.header, 10)
		handled = true
	case tcell.KeyHome:
		_, col := a.header.GetScrollOffset()
		a.header.ScrollTo(0, col) // 纵向：首行（保留横向 offset）
		handled = true
	case tcell.KeyEnd:
		_, col := a.header.GetScrollOffset()
		a.header.ScrollTo(1<<30, col) // 纵向：末行（保留横向 offset）
		handled = true
	}
	if !handled && event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'h':
			a.scrollTextHorizontal(a.header, -1)
			handled = true
		case 'l':
			a.scrollTextHorizontal(a.header, 1)
			handled = true
		case 'g':
			_, col := a.header.GetScrollOffset()
			a.header.ScrollTo(0, col) // 纵向：首行
			handled = true
		case 'G':
			_, col := a.header.GetScrollOffset()
			a.header.ScrollTo(1<<30, col) // 纵向：末行
			handled = true
		case '0':
			row, _ := a.header.GetScrollOffset()
			a.header.ScrollTo(row, 0) // 横向：行首
			handled = true
		case '$':
			row, _ := a.header.GetScrollOffset()
			a.header.ScrollTo(row, 1<<30) // 横向：行尾
			handled = true
		}
	}
	return handled
}

func (a *App) handleTableIdleKey(event *tcell.EventKey) *tcell.EventKey {
	// 横向滚动（仅命令区）
	handled := false
	switch event.Key() {
	case tcell.KeyLeft:
		a.scrollCmdViewHorizontal(-1)
		handled = true
	case tcell.KeyRight:
		a.scrollCmdViewHorizontal(1)
		handled = true
	}
	if !handled && event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'h':
			a.scrollCmdViewHorizontal(-1)
			handled = true
		case 'l':
			a.scrollCmdViewHorizontal(1)
			handled = true
		case '0':
			a.cmdView.ScrollTo(0, 0) // 横向：行首
			handled = true
		case '$':
			a.cmdView.ScrollTo(0, 1<<30) // 横向：行尾
			handled = true
		}
	}
	if handled {
		return nil
	}

	if a.handleTableNav(event) {
		return nil
	}

	// ...（后面保持不变）

	switch event.Key() {
	case tcell.KeyEnter:
		a.runSelectedTasks()
		return nil
	}
	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case ' ':
			a.toggleCurrent()
			return nil
		case 'a', 'A':
			a.selectAll(true)
			return nil
		case 'n', 'N':
			a.selectAll(false)
			return nil
		case 'd', 'D':
			a.dryRunSelected()
			return nil
		case 'm', 'M':
			a.runMountCheck()
			return nil
		case 'r', 'R':
			a.reloadConfig()
			return nil
		}
	}
	return event
}

func (a *App) handleInteractIdleKey(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'p', 'P', ' ':
			a.togglePause()
			return nil
		}
	}
	if a.handleLogScroll(event) {
		return nil
	}
	return nil
}

// ---------- 暂停 ----------
func (a *App) togglePause() {
	newPaused := !a.paused.Load()
	a.paused.Store(newPaused)
	if newPaused {
		a.updateStatusLine1()
	} else {
		a.flushLogBuf()
		a.logPart.ScrollToEnd()
		a.updateStatusLine1()
	}
}

// ---------- 选择操作 ----------
func (a *App) toggleCurrent() {
	row, _ := a.table.GetSelection()
	if row <= 0 || row > len(a.cfg.Tasks) {
		return
	}
	t := a.cfg.Tasks[row-1]
	t.Selected = !t.Selected
	a.refreshTasks()
	a.table.Select(row, 0)
	a.updateStatusLine1()
}

func (a *App) selectAll(sel bool) {
	for _, t := range a.cfg.Tasks {
		t.Selected = sel
	}
	row, _ := a.table.GetSelection()
	a.refreshTasks()
	if row > 0 && row <= len(a.cfg.Tasks) {
		a.table.Select(row, 0)
	}
	a.updateStatusLine1()
}

func (a *App) selectedTasks() []*Task {
	var out []*Task
	for _, t := range a.cfg.Tasks {
		if t.Selected {
			out = append(out, t)
		}
	}
	return out
}

// ---------- 运行触发 ----------
func (a *App) runSelectedTasks() {
	if a.running || a.confirmState != nil {
		return
	}
	sel := a.selectedTasks()
	if len(sel) == 0 {
		a.setStatus("[yellow]未选择任何任务[-]")
		return
	}
	a.planRun(sel, false)
}

func (a *App) dryRunSelected() {
	if a.running || a.confirmState != nil {
		return
	}
	sel := a.selectedTasks()
	if len(sel) == 0 {
		a.setStatus("[yellow]未选择任何任务[-]")
		return
	}
	a.planRun(sel, true)
}

func (a *App) planRun(tasks []*Task, dryRun bool) {
	var dangerous, safe []*Task
	for _, t := range tasks {
		if t.IsDangerous(a.cfg.Global) {
			dangerous = append(dangerous, t)
		} else {
			safe = append(safe, t)
		}
	}
	if len(dangerous) == 0 {
		a.startRun(tasks, dryRun)
		return
	}
	a.confirmState = &ConfirmState{
		Queue:        dangerous,
		Index:        0,
		Decisions:    make(map[string]bool),
		NonDangerous: safe,
		DryRun:       dryRun,
	}
	a.interactState = InteractConfirming

	a.logPart.Clear()
	a.showConfirmStep()
	a.updateStatusBar()
}

func (a *App) showConfirmStep() {
	cs := a.confirmState
	if cs == nil {
		return
	}
	if cs.Index >= len(cs.Queue) {
		a.finishConfirm()
		return
	}

	task := cs.Queue[cs.Index]
	total := len(cs.Queue)
	idx := cs.Index + 1

	sep := strings.Repeat("-", 80)
	fmt.Fprintln(a.logPart, "[gray]"+sep+"[-]")
	fmt.Fprintf(a.logPart, "[red::b]⚠ 危险确认 %d/%d[-:-:-]\n", idx, total)
	fmt.Fprintf(a.logPart, "  [yellow]任务:[-] %s\n", task.Name)

	var flags []string
	if task.NeedsDelete() {
		flags = append(flags, "--delete")
	}
	if task.NeedsRemoveSource(a.cfg.Global) {
		flags = append(flags, "--remove-source-files")
	}
	fmt.Fprintf(a.logPart, "  [yellow]危险选项:[-] %s\n", strings.Join(flags, ", "))

	if task.NeedsDelete() {
		fmt.Fprintln(a.logPart, "  [yellow]说明:[-] --delete 会删除目标端多余文件（进回收站）")
	}
	if task.NeedsRemoveSource(a.cfg.Global) {
		fmt.Fprintln(a.logPart, "  [red]说明: --remove-source-files 会删除本地源文件（不可恢复）[-]")
	}
	fmt.Fprintln(a.logPart, "")
	fmt.Fprintln(a.logPart, "  [white]y[-]=确认  [white]n[-]=跳过  [white]a[-]=全部确认  [white]s[-]=全部跳过")
	fmt.Fprintln(a.logPart, "[gray]"+sep+"[-]")
	a.logPart.ScrollToEnd()

	a.startStatusBlink(fmt.Sprintf("⚠ 等待确认 %d/%d", idx, total))

	for i, t := range a.cfg.Tasks {
		if t.Name == task.Name {
			a.table.Select(i+1, 0)
			break
		}
	}
	a.refreshTasks()
	a.setFocus("interact")
	a.updateStatusBar()
}

func (a *App) confirmCurrent(confirm bool) {
	cs := a.confirmState
	if cs == nil || cs.Index >= len(cs.Queue) {
		return
	}
	task := cs.Queue[cs.Index]
	cs.Decisions[task.Name] = confirm
	if confirm {
		fmt.Fprintln(a.logPart, "[green]→ 已确认[-]")
	} else {
		fmt.Fprintln(a.logPart, "[yellow]→ 已跳过[-]")
	}
	a.logPart.ScrollToEnd()
	cs.Index++
	a.showConfirmStep()
}

func (a *App) confirmAllConfirm() {
	cs := a.confirmState
	if cs == nil {
		return
	}
	fmt.Fprintln(a.logPart, "[green]→ 已全部确认[-]")
	for i := cs.Index; i < len(cs.Queue); i++ {
		cs.Decisions[cs.Queue[i].Name] = true
	}
	a.logPart.ScrollToEnd()
	a.finishConfirm()
}

func (a *App) confirmAllSkip() {
	cs := a.confirmState
	if cs == nil {
		return
	}
	fmt.Fprintln(a.logPart, "[yellow]→ 已全部跳过[-]")
	for i := cs.Index; i < len(cs.Queue); i++ {
		cs.Decisions[cs.Queue[i].Name] = false
	}
	a.logPart.ScrollToEnd()
	a.finishConfirm()
}

func (a *App) finishConfirm() {
	cs := a.confirmState
	a.stopStatusBlink()
	a.confirmState = nil
	a.refreshTasks()

	var toRun []*Task
	for _, t := range cs.Queue {
		if cs.Decisions[t.Name] {
			toRun = append(toRun, t)
		}
	}
	toRun = append(toRun, cs.NonDangerous...)

	if len(toRun) == 0 {
		fmt.Fprintln(a.logPart, "[red]无可执行任务，返回主界面。[-]")
		a.logPart.ScrollToEnd()
		a.interactState = InteractIdle
		a.setFocus("table")
		a.setStatus("[yellow]无可执行任务[-]")
		return
	}

	fmt.Fprintln(a.logPart, "")
	fmt.Fprintf(a.logPart, "[yellow]开始执行 %d 个任务...[-]\n", len(toRun))
	a.logPart.ScrollToEnd()

	go func() {
		time.Sleep(500 * time.Millisecond)
		a.app.QueueUpdateDraw(func() {
			a.startRun(toRun, cs.DryRun)
		})
	}()
}

// ---------- 执行 ----------
func (a *App) startRun(tasks []*Task, dryRun bool) {
	if a.running {
		return
	}
	a.running = true
	a.paused.Store(false)
	a.cancelRequested = false
	a.interactState = InteractRunning
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	mode := "实际执行"
	if dryRun {
		mode = "预览"
	}

	a.runStartTime = time.Now()
	a.runCurrent = 0
	a.runTotal = len(tasks)
	a.runCurrentTask = "-"
	a.runSuccess = 0
	a.runSkipped = 0
	a.runFailed = 0
	a.runMountFailed = 0

	a.logPart.Clear()
	fmt.Fprintf(a.logPart, "[cyan][%s] 开始%s：共 %d 个任务[-]\n",
		time.Now().Format("15:04:05"), mode, len(tasks))
	fmt.Fprintln(a.logPart, tview.Escape("正在准备..."))
	fmt.Fprintln(a.logPart, "")

	a.setFocus("interact")
	a.updateStatusBar()
	go a.doRun(ctx, tasks, dryRun, mode)
}

func (a *App) doRun(ctx context.Context, tasks []*Task, dryRun bool, mode string) {
	defer func() {
		a.app.QueueUpdateDraw(func() {
			a.running = false
			a.cancel = nil
			a.cancelRequested = false
		})
	}()

	runStart := time.Now()

	success, failed, skipped, mountFailed := 0, 0, 0, 0
	total := len(tasks)
	cancelled := false

	var cumFilesXfer, cumFilesTotal int
	var cumBytesTotal, cumBytesSent, cumBytesRecv int64
	var cumRsyncMS, cumListMS int64

	var sName, skName, fName, mName []string

	for i, t := range tasks {
		i, t := i, t

		taskStart := time.Now()

		a.errMu.Lock()
		a.currentErrors = nil
		a.errMu.Unlock()

		a.app.QueueUpdateDraw(func() {
			a.runCurrent = i + 1
			a.runCurrentTask = t.Name
			a.updateStatusLine1()
		})

		var res *TaskResult
		if dryRun {
			res = StreamDryRun(ctx, a.scriptPath, a.configPath, t.Name, a.outputLine)
		} else {
			res = StreamTask(ctx, a.scriptPath, a.configPath, t.Name, a.outputLine)
		}

		taskDuration := formatDuration(time.Since(taskStart))

		var result string
		switch {
		case res.Err != nil || res.ExitCode < 0:
			result = "失败"
			failed++
			fName = append(fName, t.Name)
		case res.ExitCode == 0:
			result = "成功"
			success++
			sName = append(sName, t.Name)
		case res.ExitCode == 2:
			result = "跳过"
			skipped++
			skName = append(skName, t.Name)
		case res.ExitCode == 3:
			result = "挂载门禁失败"
			mountFailed++
			mName = append(mName, t.Name)
		default:
			result = "失败"
			failed++
			fName = append(fName, t.Name)
		}

		if res.ExitCode == 0 && res.Stats != nil {
			cumFilesXfer += res.Stats.FilesTransferred
			cumFilesTotal += res.Stats.FilesTotal
			cumBytesTotal += res.Stats.BytesTotal
			cumBytesSent += res.Stats.BytesSent
			cumBytesRecv += res.Stats.BytesReceived
			cumRsyncMS += res.Stats.RsyncMS
			cumListMS += res.Stats.ListMS
		}

		a.errMu.Lock()
		errs := append([]string(nil), a.currentErrors...)
		a.currentErrors = nil
		a.errMu.Unlock()

		a.app.QueueUpdateDraw(func() {
			a.runSuccess = success
			a.runSkipped = skipped
			a.runFailed = failed
			a.runMountFailed = mountFailed

			var color string
			switch result {
			case "成功":
				color = "[green]成功[-]"
			case "跳过":
				color = "[yellow]跳过[-]"
			case "挂载门禁失败":
				color = "[orange]挂载门禁失败[-]"
			default:
				color = "[red]失败[-]"
			}

			// 第 1 行：任务名 + 结果 + 用时 + 累积
			fmt.Fprintf(a.logPart,
				"\n[cyan]>>> [%d/%d] %s %s[-]    [gray]用时:[-] %s    "+
					"[gray]累积:[-] [green]成功 %d[-]  [yellow]跳过 %d[-]  "+
					"[red]失败 %d[-]  [orange]挂载门禁失败 %d[-]\n",
				i+1, total, t.Name, color, taskDuration,
				success, skipped, failed, mountFailed)

			// 第 2 行：文件 + 总大小 + 数据 + 速率 + 列表 + 执行
			if res.Stats != nil && res.ExitCode == 0 {
				rate := formatRate(res.Stats.BytesSent+res.Stats.BytesReceived, res.Stats.RsyncMS)
				listStr := formatDuration(time.Duration(res.Stats.ListMS) * time.Millisecond)
				rsyncStr := formatDuration(time.Duration(res.Stats.RsyncMS) * time.Millisecond)
				line := fmt.Sprintf(
					"[gray]      传输: 文件: %d/%d  总大小: %s  数据: 发送 %s + 接收 %s  速率: %s  列表: %s  执行: %s",
					res.Stats.FilesTransferred, res.Stats.FilesTotal,
					formatBytes(res.Stats.BytesTotal),
					formatBytes(res.Stats.BytesSent),
					formatBytes(res.Stats.BytesReceived),
					rate, listStr, rsyncStr)
				fmt.Fprintln(a.logPart, line+"[-]")
			}

			if len(errs) > 0 {
				max := 5
				if len(errs) < max {
					max = len(errs)
				}
				fmt.Fprintln(a.logPart, "[red]    错误摘要：[-]")
				for j := 0; j < max; j++ {
					fmt.Fprintf(a.logPart, "[red]      %s[-]\n", tview.Escape(errs[j]))
				}
				if len(errs) > max {
					fmt.Fprintf(a.logPart,
						"[red]      ... 还有 %d 行[-]\n", len(errs)-max)
				}
			}
			fmt.Fprintln(a.logPart, "")
			if !a.paused.Load() {
				a.logPart.ScrollToEnd()
			}
			a.updateStatusBar()
		})

		select {
		case <-ctx.Done():
			cancelled = true
			goto finish
		default:
		}
	}

finish:
	a.app.QueueUpdateDraw(func() {
		runDuration := time.Since(runStart)

		if cancelled {
			a.interactState = InteractCancelled
		} else {
			a.interactState = InteractDone
		}

		a.lastSuccess = success
		a.lastSkipped = skipped
		a.lastFailed = failed
		a.lastMountFailed = mountFailed
		a.lastRunDuration = runDuration

		sep := strings.Repeat("-", 80)
		fmt.Fprintln(a.logPart, "")
		fmt.Fprintln(a.logPart, "[cyan]"+sep+"[-]")

		// 第 1 行：完成状态 + 总用时
		if cancelled {
			fmt.Fprintf(a.logPart,
				" [yellow][%s] 运行被取消（总用时 %s）[-]\n",
				time.Now().Format("15:04:05"), formatDuration(runDuration))
		} else if failed > 0 {
			fmt.Fprintf(a.logPart,
				" [red][%s] 有任务失败（总用时 %s）[-]\n",
				time.Now().Format("15:04:05"), formatDuration(runDuration))
		} else if mountFailed > 0 {
			fmt.Fprintf(a.logPart,
				" [orange][%s] 有任务因挂载门禁失败（总用时 %s）[-]\n",
				time.Now().Format("15:04:05"), formatDuration(runDuration))
		} else {
			fmt.Fprintf(a.logPart,
				" [green][%s] 全部任务完成（总用时 %s）[-]\n",
				time.Now().Format("15:04:05"), formatDuration(runDuration))
		}

		// 第 2 行：成功 / 跳过 / 失败 / 挂载门禁失败（带任务名列表）
		var parts []string
		if len(sName) > 0 {
			parts = append(parts, fmt.Sprintf("[green]成功 %d: %s[-]",
				len(sName), strings.Join(sName, " ")))
		} else {
			parts = append(parts, "[green]成功 0[-]")
		}
		if len(skName) > 0 {
			parts = append(parts, fmt.Sprintf("[yellow]跳过 %d: %s[-]",
				len(skName), strings.Join(skName, " ")))
		} else {
			parts = append(parts, "[yellow]跳过 0[-]")
		}
		if len(fName) > 0 {
			parts = append(parts, fmt.Sprintf("[red]失败 %d: %s[-]",
				len(fName), strings.Join(fName, " ")))
		} else {
			parts = append(parts, "[red]失败 0[-]")
		}
		if len(mName) > 0 {
			parts = append(parts, fmt.Sprintf("[orange]挂载门禁失败 %d: %s[-]",
				len(mName), strings.Join(mName, " ")))
		} else {
			parts = append(parts, "[orange]挂载门禁失败 0[-]")
		}
		fmt.Fprintln(a.logPart, " "+strings.Join(parts, "  "))

		// 第 3 行：传输统计
		if cumFilesTotal > 0 || cumBytesSent > 0 {
			line := fmt.Sprintf(
				" [cyan]传输:[-] 文件: %d/%d  总大小: %s  数据: 发送 %s + 接收 %s",
				cumFilesXfer, cumFilesTotal, formatBytes(cumBytesTotal),
				formatBytes(cumBytesSent), formatBytes(cumBytesRecv))
			if cumRsyncMS > 0 {
				line += fmt.Sprintf("  速率: %s",
					formatRate(cumBytesSent+cumBytesRecv, cumRsyncMS))
			}
			if cumListMS > 0 {
				line += fmt.Sprintf("  列表: %s",
					formatDuration(time.Duration(cumListMS)*time.Millisecond))
			}
			if cumRsyncMS > 0 {
				line += fmt.Sprintf("  执行: %s",
					formatDuration(time.Duration(cumRsyncMS)*time.Millisecond))
			}
			fmt.Fprintln(a.logPart, line)
		}

		fmt.Fprintln(a.logPart, "[cyan]"+sep+"[-]")
		a.logPart.ScrollToEnd()

		a.updateStatusBar()
		a.updateHelp()
	})
}

func (a *App) outputLine(line string) {
	clean := ansiRe.ReplaceAllString(line, "")
	if shouldFilterLine(clean) {
		return
	}
	if isErrorLine(clean) {
		a.errMu.Lock()
		if len(a.currentErrors) < 20 {
			a.currentErrors = append(a.currentErrors, clean)
		}
		a.errMu.Unlock()
	}

	if a.paused.Load() {
		a.logBufMu.Lock()
		a.logBuf = append(a.logBuf, clean)
		a.logBufMu.Unlock()
		return
	}

	a.app.QueueUpdateDraw(func() {
		fmt.Fprintln(a.logPart, tview.Escape(clean))
		a.logPart.ScrollToEnd()
	})
}

// ---------- 挂载检查 ----------
func (a *App) runMountCheck() {
	if a.running || a.confirmState != nil {
		return
	}
	a.running = true
	a.paused.Store(false)
	a.cancelRequested = false
	a.interactState = InteractRunning
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	a.logPart.Clear()
	fmt.Fprintln(a.logPart, tview.Escape("正在检查远端挂载状态..."))
	a.setFocus("interact")
	a.updateStatusBar()

	go func() {
		defer func() {
			a.app.QueueUpdateDraw(func() {
				a.running = false
				a.cancel = nil
				a.cancelRequested = false
				a.interactState = InteractDone
				a.updateStatusBar()
				a.updateHelp()
			})
		}()

		res := StreamCheckMount(ctx, a.scriptPath, a.configPath, func(line string) {
			clean := ansiRe.ReplaceAllString(line, "")
			if shouldFilterLine(clean) {
				return
			}
			a.app.QueueUpdateDraw(func() {
				fmt.Fprintln(a.logPart, tview.Escape(clean))
				if !a.paused.Load() {
					a.logPart.ScrollToEnd()
				}
			})
		})

		a.app.QueueUpdateDraw(func() {
			fmt.Fprintf(a.logPart, "\n[退出码: %d]\n", res.ExitCode)
			if res.Err != nil {
				fmt.Fprintf(a.logPart, "[red]错误: %v[-]\n", res.Err)
			}
			a.logPart.ScrollToEnd()
			a.setStatus("[green]挂载检查完成[-]")
		})
	}()
}

// ---------- 重载 ----------
func (a *App) reloadConfig() {
	cfg, err := ParseConfig(a.configPath)
	if err != nil {
		a.setStatus(fmt.Sprintf("[red]重新加载失败: %v[-]", err))
		return
	}
	for _, t := range cfg.Tasks {
		if old, ok := a.cfg.TaskMap[t.Name]; ok && old.Selected {
			t.Selected = true
		}
	}
	a.cfg = cfg
	a.updateHeader()
	a.refreshTasks()
	a.updateStatusBar()
	a.setStatus("[green]配置已重新加载[-]")
}

// ---------- 帮助浮层 ----------
func (a *App) helpContent() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(
		"[yellow::b]rbackup-ui[-:-:-]  [white]%s[-]  [gray](git: %s, 构建: %s)[-]\n",
		Version, GitCommit, BuildTime))
	b.WriteString("\n")
	b.WriteString("[yellow]作者:[-]  Jet Locke\n")
	b.WriteString("\n")

	kv := func(key, desc string) string {
		return fmt.Sprintf("  %-20s %s\n", key, desc)
	}

	b.WriteString("[yellow::b]─── 全局按键 ────────────────────────────────────────[-:-:-]\n")
	b.WriteString(kv("F1 / ?", "显示帮助"))
	b.WriteString(kv("Tab", "循环切焦点 (1→2→3→1)"))
	b.WriteString(kv("1 / 2 / 3", "直选焦点 (信息/任务/交互)"))
	b.WriteString(kv("q", "空闲时退出程序"))
	b.WriteString(kv("Esc", "空闲时退出 / interact 焦点切回 table"))
	b.WriteString(kv("Ctrl+C", "空闲退出 / 运行停止 / 确认全部跳过"))
	b.WriteString(kv("Ctrl+D ×3", "强制退出程序"))
	b.WriteString("\n")

	b.WriteString("[yellow::b]─── 焦点 1: 信息区 (header) ────────────────────────[-:-:-]\n")
	b.WriteString(kv("← → / h l", "横向滚动"))
	b.WriteString(kv("↑ ↓ / j k", "纵向滚动（保留）"))
	b.WriteString(kv("PgUp / PgDn", "翻页"))
	b.WriteString(kv("g / G", "纵向首行 / 末行"))
	b.WriteString(kv("Home / End", "纵向首行 / 末行（同 g/G）"))
	b.WriteString(kv("0 / $", "横向行首 / 行尾"))
	b.WriteString("\n")

	b.WriteString("[yellow::b]─── 焦点 2: 任务区 (table) ────────────────────────[-:-:-]\n")
	b.WriteString(kv("↑ ↓ / j k", "移动光标"))
	b.WriteString(kv("← → / h l", "命令区横向滚动"))
	b.WriteString(kv("PgUp / PgDn", "翻页"))
	b.WriteString(kv("g / G", "首行 / 末行"))
	b.WriteString(kv("Home / End", "首行 / 末行（同 g/G）"))
	b.WriteString(kv("0 / $", "命令区行首 / 行尾"))
	b.WriteString(kv("空格", "选择 / 取消选择"))
	b.WriteString(kv("a / n", "全选 / 全不选"))
	b.WriteString(kv("Enter", "运行选中任务"))
	b.WriteString(kv("d", "预览（dry-run）"))
	b.WriteString(kv("m", "挂载检查"))
	b.WriteString(kv("r", "刷新配置"))
	b.WriteString("\n")

	b.WriteString("[yellow::b]─── 焦点 3: 交互区 (interact) ──────────────────────[-:-:-]\n")
	b.WriteString(kv("↑ ↓ / j k", "滚动日志"))
	b.WriteString(kv("← → / h l", "横向滚动"))
	b.WriteString(kv("PgUp / PgDn", "翻页"))
	b.WriteString(kv("g / G", "纵向首行 / 末行"))
	b.WriteString(kv("Home / End", "纵向首行 / 末行（同 g/G）"))
	b.WriteString(kv("0 / $", "横向行首 / 行尾"))
	b.WriteString(kv("p / 空格", "暂停 / 继续自动滚动"))
	b.WriteString(kv("Esc", "切回任务区焦点"))
	b.WriteString("\n")

	b.WriteString("[yellow::b]─── 危险确认 ───────────────────────────────────────[-:-:-]\n")
	b.WriteString(kv("y", "确认当前任务"))
	b.WriteString(kv("n", "跳过当前任务"))
	b.WriteString(kv("a", "全部确认"))
	b.WriteString(kv("s", "全部跳过"))
	b.WriteString(kv("Ctrl+C", "全部跳过"))
	b.WriteString("\n")

	b.WriteString("[yellow::b]─── 关于 ────────────────────────────────────────────[-:-:-]\n")
	b.WriteString("  rbackup-tui / rbackup-web 是 rbackup-cli 的前端（终端 / 网页）\n")
	b.WriteString("  配置文件: 与 rbackup-cli 共用 config.ini\n")
	b.WriteString("  日志目录: 由 config.ini 的 LOG_DIR 决定，默认跟随平台\n")
	b.WriteString("            Linux: $XDG_STATE_HOME/rbackup-ui/log\n")
	b.WriteString("            Windows: %LOCALAPPDATA%\\rbackup-ui\\Logs\n")
	b.WriteString("\n")
	b.WriteString("[gray]按 q 或 Esc 关闭本帮助[-]\n")

	return b.String()
}

func (a *App) showHelp() {
	if a.helpVisible {
		return
	}
	a.helpVisible = true

	tv := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	tv.SetBorder(true).SetTitle(" 帮助 ")
	tv.SetText(a.helpContent())

	a.helpPage = tv
	a.pages.AddPage("help", tv, true, true)
	a.app.SetFocus(tv)
}

func (a *App) hideHelp() {
	if !a.helpVisible {
		return
	}
	a.helpVisible = false
	a.pages.RemovePage("help")
	a.helpPage = nil
	a.setFocus(a.focusArea)
}

func (a *App) updateHelp() {
	// 状态栏第 3 行固定展示全局键，此函数保留用于将来扩展
}

// ---------- 过滤与错误识别 ----------
var filterPrefixes = []string{
	"备份脚本启动 (PID:", "脚本路径:", "配置文件:", "远程主机:",
	"日志文件:", "统计文件:", "挂载门禁:", "任务选择:", "强制模式:", "提权已启用:",
	"模式: 任务模式", "模式: 临时任务", "模式: 仅挂载检查",
	"[CHECK-MOUNT]", "[DRY-RUN]", "汇总:", "跳过 0", "失败 0", "挂载门禁失败 0",
}

func shouldFilterLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "====") {
		return true
	}
	if regexp.MustCompile(`^(成功|跳过|失败|挂载门禁失败)\s+\d+`).MatchString(t) {
		return true
	}
	for _, p := range filterPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func isErrorLine(line string) bool {
	l := strings.ToLower(line)
	markers := []string{
		"rsync error", "rsync:", "[fail]", "failed", "error:",
		"错误", "失败", "拒绝", "不可恢复",
	}
	for _, m := range markers {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// ---------- main ----------
func main() {
	// Windows 中文控制台默认 GBK，先把输出代码页切到 UTF-8，
	// 否则 CLI 中文与 ✓/✗ 会乱码、列错位（TUI 由 tcell 自行处理，不受影响）。
	enableUTF8Console()

	// --doctor 要在初始化 TUI（tcell 屏幕）之前处理
	for _, a := range os.Args[1:] {
		if a == "--doctor" {
			os.Exit(runDoctorCLI())
		}
	}
	warnRuntimeDeps()
	a := NewApp()

	if webMode {
		os.Exit(runWebCLI(a.scriptPath, a.configPath, webOpts))
	}

	if debugKeys {
		fmt.Fprintf(os.Stderr, "[DEBUG] version=%s git=%s build=%s\n",
			Version, GitCommit, BuildTime)
		fmt.Fprintf(os.Stderr, "[DEBUG] script=%s\n", a.scriptPath)
		fmt.Fprintf(os.Stderr, "[DEBUG] config=%s\n", a.configPath)
	}

	if a.scriptPath == "" {
		fmt.Fprintln(os.Stderr, "错误：找不到 rbackup-cli")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "请按以下方式之一指定：")
		fmt.Fprintln(os.Stderr, "  1. 命令行: rbackup-tui -s /path/to/rbackup-cli")
		fmt.Fprintln(os.Stderr, "  2. 环境变量: export RBACKUP_SCRIPT=/path/to/rbackup-cli")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "正常情况下无需指定：引擎内嵌在二进制中，首次运行自动释放到")
		fmt.Fprintln(os.Stderr, "  <数据目录>/rbackup-ui/backend/rbackup-cli")
		fmt.Fprintln(os.Stderr, "兜底查找：PATH 中的 rbackup-cli（也可用 -s 显式指定）")
		os.Exit(1)
	}

	cfg, err := ParseConfig(a.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		fmt.Fprintf(os.Stderr, "尝试路径: %s\n", a.configPath)
		fmt.Fprintf(os.Stderr, "可通过 RBACKUP_CONFIG 或 -c 指定配置文件\n")
		os.Exit(1)
	}
	a.cfg = cfg

	a.setupUI()
	a.updateHeader()
	a.refreshTasks()
	a.updateStatusBar()
	a.updateFocusStyle()

	a.app.SetRoot(a.pages, true).EnableMouse(true)
	a.setFocus("table")

	if err := a.app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

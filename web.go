package main

// Web UI：单二进制内嵌的 HTTP 服务（--web 模式）。
//
// 设计见 docs/WEBUI-DESIGN.md。原则：
//   - 只观察 + 受控触发，不提供任意命令执行
//   - 单 job 互斥；任务串行；取消按进程组终止
//   - 默认仅监听 127.0.0.1:30031（本机端口规划：rbackup-ui=30031，gocryptfs-ui=30032）
//   - 非 loopback 监听必须 token + TLS（fail-closed）
//   - 危险任务（--delete / --remove-source-files）必须先成功 dry-run 预览

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html web/style.css web/app.js
var webAssets embed.FS

const (
	webSessionCookie = "rbs"
	webPendingCookie = "rbp"
	webCSRFHeader    = "X-RB-CSRF"
	webMaxBody       = 1 << 20
	webPreviewTTL    = 10 * time.Minute
)

// ---------- 选项 ----------

type webOptions struct {
	Addr         string
	Token        string
	TokenFile    string
	TLSCert      string
	TLSKey       string
	InsecureHTTP bool
	TrustProxy   bool   // 反向代理后信任 X-Forwarded-*（代理须自行设置/覆盖这些头）
	PublicURL    string // 对外基址（可选；未设置时按请求头/Host 推导，适配多入口）
	AuditLog     string
	MaxClients   int
	ReadOnly     bool
	SessionTTL   time.Duration

	// 认证扩展（A+B+C）
	User             string
	PasswordHash     string
	PasswordHashFile string
	TwoFA            string // off | totp
	TOTPSecretFile   string
	BackupCodesFile  string
	totpSecret       []byte
	totpSecretFile   string
	backupCodesFile  string
	backupHashes     []string
}

func defaultWebOptions() webOptions {
	return webOptions{Addr: "127.0.0.1:30031", MaxClients: 8, SessionTTL: 8 * time.Hour,
		User: "admin", TwoFA: "off"}
}

func isLoopbackHost(host string) bool {
	if host == "" {
		return false // ":30031" = 监听全部网卡
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (o *webOptions) loadCredentials() error {
	if o.TokenFile != "" {
		b, err := os.ReadFile(o.TokenFile)
		if err != nil {
			return fmt.Errorf("读取 token 文件失败: %w", err)
		}
		o.Token = strings.TrimSpace(string(b))
	}
	if o.Token == "" {
		o.Token = os.Getenv("RBACKUP_WEB_TOKEN")
	}

	if o.PasswordHashFile != "" {
		b, err := os.ReadFile(o.PasswordHashFile)
		if err != nil {
			return fmt.Errorf("读取密码哈希文件失败: %w", err)
		}
		o.PasswordHash = strings.TrimSpace(string(b))
	}
	if o.PasswordHash == "" {
		o.PasswordHash = os.Getenv("RBACKUP_WEB_PASSWORD_HASH")
	}
	if o.TwoFA == "" {
		o.TwoFA = "off"
	}
	if o.TOTPSecretFile != "" {
		o.totpSecretFile = o.TOTPSecretFile
	} else if o.TwoFA == "totp" {
		o.totpSecretFile = defaultTOTPSecretFile()
	}
	if o.totpSecretFile != "" {
		b, err := os.ReadFile(o.totpSecretFile)
		switch {
		case err == nil:
			secret, derr := decodeTOTPSecret(string(b))
			if derr != nil {
				return fmt.Errorf("TOTP secret 格式错误: %w", derr)
			}
			o.totpSecret = secret
		case errors.Is(err, os.ErrNotExist):
			// 尚未配置：允许启动，由 Web 安全页自助设置
		default:
			return fmt.Errorf("读取 TOTP secret 文件失败: %w", err)
		}
	}
	if o.BackupCodesFile != "" {
		o.backupCodesFile = o.BackupCodesFile
	} else if o.totpSecretFile != "" {
		o.backupCodesFile = o.totpSecretFile + ".backup-codes"
	}
	o.backupHashes = loadBackupHashes(o.backupCodesFile)
	return nil
}

// validate 实现 fail-closed 启动校验：不安全的组合直接拒绝启动。
func (o *webOptions) validate() error {
	if (o.TLSCert == "") != (o.TLSKey == "") {
		return errors.New("--web-tls-cert 与 --web-tls-key 必须同时提供")
	}
	if o.User == "" {
		o.User = "admin"
	}
	if o.TwoFA == "" {
		o.TwoFA = "off"
	}
	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return fmt.Errorf("--web-addr 格式错误（应形如 127.0.0.1:30031）: %v", err)
	}
	loop := isLoopbackHost(host)
	if o.Token != "" && len(o.Token) < 32 {
		return errors.New("token 太短：至少 32 个字符")
	}
	if o.PasswordHash != "" && !validPasswordHash(o.PasswordHash) {
		return errors.New("密码哈希格式错误（应为 pbkdf2-sha256$迭代数$盐$哈希，用 --web-hash-password 生成）")
	}
	switch o.TwoFA {
	case "off", "totp":
	default:
		return fmt.Errorf("--web-2fa 只支持 off 或 totp，得到 %q", o.TwoFA)
	}
	if o.TwoFA == "totp" {
		if o.PasswordHash == "" {
			return errors.New("启用 TOTP 需要同时配置密码登录（--web-password-hash-file）")
		}
		// secret 文件可以不在启动时提供：安全页可自助设置（首次/重新绑定）
	}
	hasCred := o.Token != "" || o.PasswordHash != ""
	if !loop {
		if !hasCred {
			return errors.New("监听非 loopback 地址必须提供 token 或密码（推荐 --web-token-file），已拒绝启动。\n" +
				"生成 token：openssl rand -base64 32 > /etc/rbackup-ui/web.token && chmod 600 /etc/rbackup-ui/web.token")
		}
		if o.TLSCert == "" && !o.InsecureHTTP {
			return errors.New("监听非 loopback 地址必须启用 TLS（--web-tls-cert/--web-tls-key），" +
				"或显式加 --web-insecure-http（仅限隔离内网/VPN），已拒绝启动")
		}
	}
	if o.PublicURL != "" {
		u, err := url.Parse(o.PublicURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("--web-public-url 不是有效的 URL: %q", o.PublicURL)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
		default:
			return fmt.Errorf("--web-public-url 只支持 http/https，得到 %q", u.Scheme)
		}
		if u.Path != "" && u.Path != "/" {
			return fmt.Errorf("--web-public-url 不应包含路径: %q", o.PublicURL)
		}
		o.PublicURL = strings.TrimRight(o.PublicURL, "/")
	}
	if o.SessionTTL <= 0 {
		o.SessionTTL = 8 * time.Hour
	}
	if o.MaxClients <= 0 {
		o.MaxClients = 8
	}
	return nil
}

// ---------- 会话认证 ----------

type webAuth struct {
	token []byte
	ttl   time.Duration
}

func newWebAuth(token string, ttl time.Duration) *webAuth {
	return &webAuth{token: []byte(token), ttl: ttl}
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "fallback-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *webAuth) sign(payload string) []byte {
	m := hmac.New(sha256.New, a.token)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// newSession 生成 (cookie 值, csrf 值)。
func (a *webAuth) newSession() (string, string) {
	csrf := randToken(18)
	payload := fmt.Sprintf("%d.%s.%s", time.Now().Add(a.ttl).Unix(), randToken(9), csrf)
	b64 := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := base64.RawURLEncoding.EncodeToString(a.sign(payload))
	return b64 + "." + mac, csrf
}

// validate 校验 cookie，返回会话绑定的 csrf。
func (a *webAuth) validate(cookieVal string) (string, bool) {
	i := strings.LastIndex(cookieVal, ".")
	if i <= 0 || i == len(cookieVal)-1 {
		return "", false
	}
	b64, mac := cookieVal[:i], cookieVal[i+1:]
	payloadBytes, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return "", false
	}
	payload := string(payloadBytes)
	want := base64.RawURLEncoding.EncodeToString(a.sign(payload))
	if subtle.ConstantTimeCompare([]byte(mac), []byte(want)) != 1 {
		return "", false
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[2], true
}

func (a *webAuth) checkToken(tok string) bool {
	return subtle.ConstantTimeCompare([]byte(tok), a.token) == 1
}

// attemptLimiter 简单登录限速：同 IP 每分钟最多 10 次失败。
type attemptLimiter struct {
	mu sync.Mutex
	m  map[string]*attemptRec
}

type attemptRec struct {
	count int
	reset time.Time
}

func newAttemptLimiter() *attemptLimiter {
	return &attemptLimiter{m: map[string]*attemptRec{}}
}

func (l *attemptLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.m[ip]
	if !ok || time.Now().After(rec.reset) {
		return true
	}
	return rec.count < 10
}

func (l *attemptLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.m[ip]
	if !ok || time.Now().After(rec.reset) {
		rec = &attemptRec{reset: time.Now().Add(time.Minute)}
		l.m[ip] = rec
	}
	rec.count++
}

// ---------- 审计日志 ----------

type auditLogger struct {
	mu sync.Mutex
	w  io.Writer
	f  *os.File
}

func newAuditLogger(path string) (*auditLogger, error) {
	if path == "-" {
		return &auditLogger{w: os.Stderr}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建审计日志目录失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开审计日志失败: %w", err)
	}
	return &auditLogger{w: io.MultiWriter(os.Stderr, f), f: f}, nil
}

func (l *auditLogger) log(action, ip, ua string, extra map[string]any) {
	if l == nil {
		return
	}
	rec := map[string]any{
		"ts":     time.Now().Format(time.RFC3339),
		"action": action,
		"ip":     ip,
		"ua":     ua,
	}
	for k, v := range extra {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	_, _ = l.w.Write(append(b, '\n'))
	l.mu.Unlock()
}

func (l *auditLogger) close() {
	if l != nil && l.f != nil {
		_ = l.f.Close()
	}
}

// ---------- SSE hub ----------

type sseEvent struct {
	ID   int64
	Name string
	Data []byte
}

type sseClient struct {
	ch chan sseEvent
}

type sseHub struct {
	mu         sync.Mutex
	seq        int64
	ring       []sseEvent
	ringCap    int
	maxClients int
	clients    map[*sseClient]struct{}
}

func newSSEHub(maxClients int) *sseHub {
	return &sseHub{ringCap: 2500, maxClients: maxClients, clients: map[*sseClient]struct{}{}}
}

func (h *sseHub) publish(name string, v any) sseEvent {
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(`{}`)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	ev := sseEvent{ID: h.seq, Name: name, Data: data}
	h.ring = append(h.ring, ev)
	if len(h.ring) > h.ringCap {
		h.ring = append([]sseEvent(nil), h.ring[len(h.ring)-h.ringCap:]...)
	}
	for c := range h.clients {
		select {
		case c.ch <- ev:
		default:
			// 慢客户端：直接断开（不影响 job）
			close(c.ch)
			delete(h.clients, c)
		}
	}
	return ev
}

func (h *sseHub) subscribe(lastID int64) (c *sseClient, replay []sseEvent, reset bool, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.maxClients {
		return nil, nil, false, false
	}
	c = &sseClient{ch: make(chan sseEvent, 256)}
	h.clients[c] = struct{}{}
	if lastID > 0 && len(h.ring) > 0 {
		if h.ring[0].ID > lastID+1 {
			reset = true
		} else {
			for _, ev := range h.ring {
				if ev.ID > lastID {
					replay = append(replay, ev)
				}
			}
		}
	}
	return c, replay, reset, true
}

func (h *sseHub) unsubscribe(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.ch)
	}
}

func writeSSE(w io.Writer, ev sseEvent) error {
	if ev.ID >= 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", ev.ID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data); err != nil {
		return err
	}
	return nil
}

func (h *sseHub) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, http.StatusInternalServerError, "internal", "响应不支持流式")
		return
	}
	var lastID int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		lastID, _ = strconv.ParseInt(v, 10, 64)
	}
	lastID = maxInt64(lastID, queryInt64(r, "since"))

	c, replay, reset, okSub := h.subscribe(lastID)
	if !okSub {
		jsonErr(w, http.StatusServiceUnavailable, "too_many_clients", "SSE 客户端数量已达上限")
		return
	}
	defer h.unsubscribe(c)

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream; charset=utf-8")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if reset {
		_ = writeSSE(w, sseEvent{ID: -1, Name: "reset", Data: []byte(`{"reason":"buffer_overflow"}`)})
	}
	for _, ev := range replay {
		if err := writeSSE(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case ev, ok := <-c.ch:
			if !ok {
				return
			}
			if err := writeSSE(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func queryInt64(r *http.Request, key string) int64 {
	v, _ := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	return v
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ---------- Job 管理 ----------

type jobKind string

const (
	jobKindRun        jobKind = "run"
	jobKindDryRun     jobKind = "dryrun"
	jobKindMountCheck jobKind = "mountcheck"
)

type jobSnapshot struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Tasks        []string   `json:"tasks"`
	DryRun       bool       `json:"dryRun"`
	State        string     `json:"state"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	CurrentTask  string     `json:"currentTask,omitempty"`
	Finished     int        `json:"finished"`
	Total        int        `json:"total"`
	PreviewToken string     `json:"previewToken,omitempty"`
	Error        string     `json:"error,omitempty"`
}

func (s *jobSnapshot) clone() *jobSnapshot {
	if s == nil {
		return nil
	}
	c := *s
	c.Tasks = append([]string(nil), s.Tasks...)
	if s.FinishedAt != nil {
		t := *s.FinishedAt
		c.FinishedAt = &t
	}
	return &c
}

type previewGrant struct {
	key   string
	token string
	at    time.Time
}

type webJobs struct {
	mu       sync.Mutex
	cur      *jobRun
	last     *jobSnapshot
	preview  *previewGrant
	hub      *sseHub
	audit    *auditLogger
	script   string
	confPath string
	seq      int
}

type jobRun struct {
	snap   *jobSnapshot
	cancel context.CancelFunc
	done   chan struct{}
}

var errJobBusy = errors.New("busy")

func newWebJobs(script, configPath string, hub *sseHub, audit *auditLogger) *webJobs {
	return &webJobs{hub: hub, audit: audit, script: script, confPath: configPath}
}

// tasksKey 用于绑定"预览"与"要执行的危险任务集合"。
func tasksKey(names []string) string {
	cp := append([]string(nil), names...)
	sort.Strings(cp)
	return strings.Join(cp, "\x00")
}

func (j *webJobs) setPreview(names []string) string {
	tok := randToken(18)
	j.mu.Lock()
	j.preview = &previewGrant{key: tasksKey(names), token: tok, at: time.Now()}
	j.mu.Unlock()
	return tok
}

func (j *webJobs) previewValid(names []string, token string) bool {
	if token == "" {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.preview == nil || time.Since(j.preview.at) > webPreviewTTL {
		return false
	}
	return j.preview.key == tasksKey(names) && subtle.ConstantTimeCompare([]byte(j.preview.token), []byte(token)) == 1
}

func (j *webJobs) snapshots() (cur, last *jobSnapshot) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cur != nil {
		cur = j.cur.snap.clone()
	}
	return cur, j.last.clone()
}

// start 启动一个 job；任务名必须已在 handler 侧校验过。
func (j *webJobs) start(kind jobKind, cfg *Config, names []string, dryRun, confirm bool, previewToken string) (*jobSnapshot, error) {
	if kind == jobKindRun {
		var dangerous []string
		for _, n := range names {
			t := cfg.TaskMap[n]
			if t != nil && t.IsDangerous(cfg.Global) {
				dangerous = append(dangerous, n)
			}
		}
		if len(dangerous) > 0 {
			if !confirm {
				return nil, errNeedConfirm
			}
			if !j.previewValid(names, previewToken) {
				return nil, errNeedPreview
			}
		}
	}

	j.mu.Lock()
	if j.cur != nil {
		j.mu.Unlock()
		return nil, errJobBusy
	}
	j.seq++
	now := time.Now()
	if kind == jobKindDryRun {
		dryRun = true
	}
	snap := &jobSnapshot{
		ID:        fmt.Sprintf("j-%s-%d", now.Format("20060102-150405"), j.seq),
		Kind:      string(kind),
		Tasks:     append([]string(nil), names...),
		DryRun:    dryRun,
		State:     "running",
		StartedAt: now,
		Total:     len(names),
	}
	if kind == jobKindMountCheck {
		snap.Total = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	cur := &jobRun{snap: snap, cancel: cancel, done: make(chan struct{})}
	j.cur = cur
	j.mu.Unlock()

	j.hub.publish("job_state", map[string]any{"jobId": snap.ID, "state": "running", "kind": string(kind)})
	go j.run(ctx, cfg, cur, kind, names, dryRun)
	return snap.clone(), nil
}

var (
	errNeedConfirm = errors.New("confirm_required")
	errNeedPreview = errors.New("preview_required")
)

func (j *webJobs) run(ctx context.Context, cfg *Config, cur *jobRun, kind jobKind, names []string, dryRun bool) {
	snap := cur.snap

	if kind == jobKindMountCheck {
		ok := j.runMountCheck(ctx, cur)
		state := "done"
		if ctx.Err() != nil {
			state = "canceled"
		} else if !ok {
			state = "failed"
		}
		j.finish(cur, state, "")
		return
	}

	allOK := true
	canceled := false
	var cumFiles, cumFilesTotal int
	var cumBytesTotal, cumBytesSent, cumBytesRecv int64
	var cumRsyncMS, cumListMS int64

	for idx, name := range names {
		if ctx.Err() != nil {
			canceled = true
			break
		}
		task := cfg.TaskMap[name]
		if task == nil {
			allOK = false
			break
		}
		j.mu.Lock()
		snap.CurrentTask = name
		j.mu.Unlock()
		j.hub.publish("task_start", map[string]any{
			"task": name, "index": idx + 1, "total": len(names), "dryRun": dryRun,
		})

		taskStart := time.Now()
		onLine := func(line string) {
			line = cleanWebLogLine(line)
			if line == "" {
				return
			}
			j.hub.publish("log", map[string]any{"task": name, "line": line, "stream": "out"})
		}
		var res *TaskResult
		if dryRun {
			res = StreamDryRun(ctx, j.script, j.confPath, name, onLine)
		} else {
			res = StreamTask(ctx, j.script, j.confPath, name, onLine)
		}
		if ctx.Err() != nil {
			canceled = true
		}
		result := "success"
		if canceled {
			result = "canceled"
		} else if res.Err != nil || res.ExitCode != 0 {
			result = "failed"
			allOK = false
		}

		payload := map[string]any{
			"task":       name,
			"result":     result,
			"exitCode":   res.ExitCode,
			"durationMs": time.Since(taskStart).Milliseconds(),
			"duration":   formatDuration(time.Since(taskStart)),
		}
		if res.Stats != nil {
			payload["stats"] = statsPayload(res.Stats)
			cumFiles += res.Stats.FilesTransferred
			cumFilesTotal += res.Stats.FilesTotal
			cumBytesTotal += res.Stats.BytesTotal
			cumBytesSent += res.Stats.BytesSent
			cumBytesRecv += res.Stats.BytesReceived
			cumRsyncMS += res.Stats.RsyncMS
			cumListMS += res.Stats.ListMS
		}
		j.hub.publish("task_result", payload)

		j.mu.Lock()
		snap.Finished = idx + 1
		j.mu.Unlock()
		if canceled {
			break
		}
	}

	j.hub.publish("job_summary", map[string]any{
		"files": cumFiles, "filesTotal": cumFilesTotal,
		"totalSize": formatBytes(cumBytesTotal),
		"data":      formatBytes(cumBytesSent) + " / " + formatBytes(cumBytesRecv),
		"rate":      formatRate(cumBytesSent+cumBytesRecv, cumRsyncMS),
		"rsync":     formatDuration(time.Duration(cumRsyncMS) * time.Millisecond),
		"list":      formatDuration(time.Duration(cumListMS) * time.Millisecond),
	})

	state := "done"
	if canceled {
		state = "canceled"
	} else if !allOK {
		state = "failed"
	}
	preview := ""
	if state == "done" && dryRun {
		preview = j.setPreview(names)
	}
	j.finish(cur, state, preview)
}

func (j *webJobs) runMountCheck(ctx context.Context, cur *jobRun) bool {
	j.hub.publish("task_start", map[string]any{
		"task": "__mountcheck__", "index": 1, "total": 1, "dryRun": true,
	})
	start := time.Now()
	onLine := func(line string) {
		line = cleanWebLogLine(line)
		if line == "" {
			return
		}
		j.hub.publish("log", map[string]any{"task": "__mountcheck__", "line": line, "stream": "out"})
	}
	res := StreamCheckMount(ctx, j.script, j.confPath, onLine)
	result := "success"
	if ctx.Err() != nil {
		result = "canceled"
	} else if res.Err != nil || res.ExitCode != 0 {
		result = "failed"
	}
	j.hub.publish("task_result", map[string]any{
		"task": "__mountcheck__", "result": result, "exitCode": res.ExitCode,
		"durationMs": time.Since(start).Milliseconds(), "duration": formatDuration(time.Since(start)),
	})
	return result == "success"
}

func statsPayload(s *TaskStats) map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{
		"filesTransferred": s.FilesTransferred,
		"filesTotal":       s.FilesTotal,
		"totalSize":        formatBytes(s.BytesTotal),
		"data":             formatBytes(s.BytesSent) + " / " + formatBytes(s.BytesReceived),
		"rate":             formatRate(s.BytesSent+s.BytesReceived, s.RsyncMS),
		"rsync":            formatDuration(time.Duration(s.RsyncMS) * time.Millisecond),
		"list":             formatDuration(time.Duration(s.ListMS) * time.Millisecond),
		"prep":             formatDuration(time.Duration(s.PrepMS) * time.Millisecond),
	}
}

// finish 收尾：更新快照、置终态、广播、清理当前 job。
func (j *webJobs) finish(cur *jobRun, state, previewToken string) {
	snap := cur.snap
	now := time.Now()
	j.mu.Lock()
	snap.State = state
	snap.FinishedAt = &now
	if previewToken != "" {
		snap.PreviewToken = previewToken
	}
	j.last = snap.clone()
	j.cur = nil
	j.mu.Unlock()

	close(cur.done)
	payload := map[string]any{
		"jobId": snap.ID, "state": state, "kind": snap.Kind,
		"finishedAt": now.Format(time.RFC3339),
	}
	if previewToken != "" {
		payload["previewToken"] = previewToken
	}
	j.hub.publish("job_state", payload)
	j.audit.log("job.finish", "", "", map[string]any{
		"jobId": snap.ID, "kind": snap.Kind, "state": state, "tasks": snap.Tasks,
	})
}

func (j *webJobs) cancel() (*jobSnapshot, bool) {
	j.mu.Lock()
	cur := j.cur
	if cur == nil {
		j.mu.Unlock()
		return nil, false
	}
	cur.snap.State = "canceling"
	snap := cur.snap.clone()
	cancel := cur.cancel
	j.mu.Unlock()

	j.hub.publish("job_state", map[string]any{"jobId": snap.ID, "state": "canceling"})
	cancel()
	return snap, true
}

// shutdown 取消并等待当前 job（优雅退出用）。
func (j *webJobs) shutdown(timeout time.Duration) {
	j.mu.Lock()
	cur := j.cur
	j.mu.Unlock()
	if cur == nil {
		return
	}
	_, _ = j.cancel()
	select {
	case <-cur.done:
	case <-time.After(timeout):
	}
}

// ---------- 日志行清洗 ----------

func cleanWebLogLine(line string) string {
	line = ansiRe.ReplaceAllString(line, "")
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	line = strings.TrimRight(line, " \t")
	return line
}

// ---------- HTTP 服务 ----------

type webServer struct {
	opts       webOptions
	scriptPath string
	configPath string
	auth       *webAuth
	hub        *sseHub
	jobs       *webJobs
	audit      *auditLogger
	limiter    *attemptLimiter
	handler    http.Handler

	// 认证扩展：凭据可在运行时由 2FA 管理接口更新
	credMu   sync.RWMutex
	cred     credState
	pending  *pending2FA
	totpMu   sync.Mutex
	totpLast int64

	// 手机扫码登录：一次性链接
	phoneMu    sync.Mutex
	phoneLinks map[string]*phoneLink
}

func newWebServer(o webOptions, scriptPath, configPath string) (*webServer, error) {
	if err := o.loadCredentials(); err != nil {
		return nil, err
	}
	if err := o.validate(); err != nil {
		return nil, err
	}

	logDir := defaultLogDir()
	if cfg, err := ParseConfig(configPath); err == nil {
		logDir = resolveLogDir(cfg.Global.LogDir, scriptPath)
	}
	auditPath := o.AuditLog
	if auditPath == "" {
		auditPath = filepath.Join(logDir, "web-audit.log")
	}
	audit, err := newAuditLogger(auditPath)
	if err != nil {
		return nil, err
	}

	// loopback 且未配置 token 时，仍用随机secret签名会话（重启失效），
	// 避免"空 key 的 HMAC 可被伪造"。
	hmacSecret := o.Token
	tokenFree := hmacSecret == ""
	if tokenFree {
		hmacSecret = randToken(32)
	}
	s := &webServer{
		opts:       o,
		scriptPath: scriptPath,
		configPath: configPath,
		auth:       newWebAuth(hmacSecret, o.SessionTTL),
		hub:        newSSEHub(o.MaxClients),
		audit:      audit,
		limiter:    newAttemptLimiter(),
		pending:    newPending2FA(),
		phoneLinks: map[string]*phoneLink{},
		cred: credState{
			user:         o.User,
			passwordHash: o.PasswordHash,
			twofaPolicy:  o.TwoFA == "totp",
			totpSecret:   o.totpSecret,
			totpEnabled:  o.TwoFA == "totp" && len(o.totpSecret) > 0,
			backupHashes: o.backupHashes,
			secretFile:   o.totpSecretFile,
			backupFile:   o.backupCodesFile,
		},
	}
	s.jobs = newWebJobs(scriptPath, configPath, s.hub, audit)
	s.handler = s.routes()
	return s, nil
}

func (s *webServer) close() {
	s.audit.close()
}

// ---------- 中间件与通用响应 ----------

type csrfCtxKey struct{}

func jsonWrite(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, errCode, msg string) {
	jsonWrite(w, code, map[string]any{"error": map[string]string{"code": errCode, "message": msg}})
}

func (s *webServer) commonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

func (s *webServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(webSessionCookie)
		if err != nil {
			jsonErr(w, http.StatusUnauthorized, "unauthorized", "未登录")
			return
		}
		csrf, ok := s.auth.validate(c.Value)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized", "会话已失效，请重新登录")
			return
		}
		ctx := context.WithValue(r.Context(), csrfCtxKey{}, csrf)
		next(w, r.WithContext(ctx))
	}
}

// csrf 用于写操作：同源校验 + 会话 CSRF 头校验。
func (s *webServer) csrf(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "csrf", "请求来源不被允许")
			return
		}
		csrf, _ := r.Context().Value(csrfCtxKey{}).(string)
		h := r.Header.Get(webCSRFHeader)
		if csrf == "" || subtle.ConstantTimeCompare([]byte(h), []byte(csrf)) != 1 {
			jsonErr(w, http.StatusForbidden, "csrf", "缺少或错误的 CSRF 令牌")
			return
		}
		next(w, r)
	}
}

// firstHeaderValue 取逗号分隔头部的第一个值（多级代理时最左/最外层）。
func firstHeaderValue(v string) string {
	if v == "" {
		return ""
	}
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// requestScheme 返回客户端实际使用的协议：信任代理时读 X-Forwarded-Proto，
// 否则以本地 TLS 状态为准。
func (s *webServer) requestScheme(r *http.Request) string {
	if s.opts.TrustProxy {
		if p := strings.ToLower(firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))); p != "" {
			if p == "http" || p == "https" {
				return p
			}
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// effectiveHost 返回客户端看到的主机名：信任代理时优先 X-Forwarded-Host。
func (s *webServer) effectiveHost(r *http.Request) string {
	if s.opts.TrustProxy {
		if h := firstHeaderValue(r.Header.Get("X-Forwarded-Host")); h != "" {
			return h
		}
	}
	return r.Host
}

// publicBase 返回对外基址（scheme://host），优先 --web-public-url。
func (s *webServer) publicBase(r *http.Request) string {
	if s.opts.PublicURL != "" {
		return s.opts.PublicURL
	}
	return s.requestScheme(r) + "://" + s.effectiveHost(r)
}

// requestIsSecure 判断客户端到本服务的连接是否 HTTPS（用于 cookie Secure）。
func (s *webServer) requestIsSecure(r *http.Request) bool {
	if s.opts.PublicURL != "" {
		return strings.HasPrefix(strings.ToLower(s.opts.PublicURL), "https://")
	}
	return s.requestScheme(r) == "https"
}

// sameOrigin 做同源校验：Origin 必须与当前入口（或 --web-public-url）一致。
func (s *webServer) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" {
			return false
		}
		allowed := []string{s.effectiveHost(r)}
		if s.opts.PublicURL != "" {
			if pu, err := url.Parse(s.opts.PublicURL); err == nil && pu.Host != "" {
				allowed = append(allowed, pu.Host)
			}
		}
		ok := false
		for _, h := range allowed {
			if strings.EqualFold(u.Host, h) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" && sf != "none" {
		return false
	}
	return true
}

// clientIP 返回客户端 IP：信任代理时优先 X-Forwarded-For 最左值，其次 X-Real-IP。
func (s *webServer) clientIP(r *http.Request) string {
	if s.opts.TrustProxy {
		if ip := firstHeaderValue(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
	}
	if ho, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ho
	}
	return r.RemoteAddr
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, webMaxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// ---------- 路由 ----------

func (s *webServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/authinfo", s.handleAuthInfo)
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/session/totp", s.handleTOTP)
	mux.HandleFunc("/api/session/phone", s.handlePhoneSession)
	mux.HandleFunc("/api/2fa", s.requireAuth(s.handle2FA))
	mux.HandleFunc("/api/2fa/setup", s.requireAuth(s.csrf(s.handle2FASetup)))
	mux.HandleFunc("/api/2fa/setup/confirm", s.requireAuth(s.csrf(s.handle2FASetupConfirm)))
	mux.HandleFunc("/api/2fa/backup-codes", s.requireAuth(s.csrf(s.handle2FABackupCodes)))
	mux.HandleFunc("/api/2fa/disable", s.requireAuth(s.csrf(s.handle2FADisable)))
	mux.HandleFunc("/api/qr.png", s.requireAuth(s.handleQR))
	mux.HandleFunc("/api/phone-login", s.requireAuth(s.csrf(s.handlePhoneLink)))
	mux.HandleFunc("/api/state", s.requireAuth(s.handleState))
	mux.HandleFunc("/api/jobs", s.requireAuth(s.csrf(s.handleJobs)))
	mux.HandleFunc("/api/jobs/current", s.requireAuth(s.handleCurrent))
	mux.HandleFunc("/api/jobs/current/cancel", s.requireAuth(s.csrf(s.handleCancel)))
	mux.HandleFunc("/api/mountcheck", s.requireAuth(s.csrf(s.handleMountCheck)))
	mux.HandleFunc("/api/events", s.requireAuth(s.handleEvents))
	mux.HandleFunc("/", s.handleStatic)
	return s.commonHeaders(mux)
}

func (s *webServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	cur, _ := s.jobs.snapshots()
	busy := cur != nil && (cur.State == "running" || cur.State == "canceling")
	jsonWrite(w, http.StatusOK, map[string]any{
		"status": "ok", "version": Version, "busy": busy,
	})
}

type sessionReq struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResp struct {
	CSRF         string `json:"csrf,omitempty"`
	Version      string `json:"version"`
	ReadOnly     bool   `json:"readOnly"`
	TokenFree    bool   `json:"tokenFree"`
	TotpRequired bool   `json:"totpRequired,omitempty"`
}

type authInfoResp struct {
	Methods  []string `json:"methods"`
	TOTP     bool     `json:"totp"`
	ReadOnly bool     `json:"readOnly"`
	Version  string   `json:"version"`
}

// tokenFree：既没有 token 也没有密码（仅 loopback 允许），本机免登录。
func (s *webServer) tokenFree() bool {
	return s.opts.Token == "" && s.creds().passwordHash == ""
}

func (s *webServer) authMethods() []string {
	out := []string{}
	if s.opts.Token != "" {
		out = append(out, "token")
	}
	if s.creds().passwordHash != "" {
		out = append(out, "password")
	}
	if len(out) == 0 {
		out = append(out, "none")
	}
	return out
}

func (s *webServer) issueSession(w http.ResponseWriter, r *http.Request) sessionResp {
	cookieVal, csrf := s.auth.newSession()
	http.SetCookie(w, &http.Cookie{
		Name:     webSessionCookie,
		Value:    cookieVal,
		Path:     "/api",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.requestIsSecure(r),
		MaxAge:   int(s.opts.SessionTTL.Seconds()),
	})
	s.audit.log("session.open", s.clientIP(r), r.UserAgent(), map[string]any{"twofa": s.creds().totpEnabled})
	return sessionResp{CSRF: csrf, Version: Version, ReadOnly: s.opts.ReadOnly, TokenFree: s.tokenFree()}
}

func (s *webServer) setPendingCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     webPendingCookie,
		Value:    id,
		Path:     "/api",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.requestIsSecure(r),
		MaxAge:   int(pending2FATTL.Seconds()),
	})
}

// handleAuthInfo 告诉登录页支持哪些登录方式（不含敏感信息）。
func (s *webServer) handleAuthInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	cred := s.creds()
	jsonWrite(w, http.StatusOK, authInfoResp{
		Methods:  s.authMethods(),
		TOTP:     cred.totpEnabled,
		ReadOnly: s.opts.ReadOnly,
		Version:  Version,
	})
}

func (s *webServer) handleSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		ip := s.clientIP(r)
		if !s.sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "csrf", "请求来源不被允许")
			return
		}
		if !s.limiter.allow(ip) {
			s.audit.log("session.rate_limited", ip, r.UserAgent(), nil)
			jsonErr(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
			return
		}
		// 本机无凭据模式：直接发会话
		if s.tokenFree() {
			jsonWrite(w, http.StatusOK, s.issueSession(w, r))
			return
		}
		var req sessionReq
		if err := decodeJSON(w, r, &req); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		// 粘贴时容易带上首尾空白/换行：token 与用户名做规范化（密码保持原样）
		req.Token = strings.TrimSpace(req.Token)
		req.Username = strings.TrimSpace(req.Username)
		// 密码登录（+ 可选 TOTP）
		cred := s.creds()
		if cred.passwordHash != "" && (req.Username != "" || req.Password != "") {
			userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(cred.user)) == 1
			passOK := verifyPassword(cred.passwordHash, req.Password)
			if !userOK || !passOK {
				s.limiter.fail(ip)
				s.audit.log("session.fail_password", ip, r.UserAgent(), nil)
				jsonErr(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码不正确")
				return
			}
			if cred.totpEnabled {
				id := s.pending.create()
				s.setPendingCookie(w, r, id)
				s.audit.log("session.pending_2fa", ip, r.UserAgent(), nil)
				jsonWrite(w, http.StatusOK, sessionResp{
					Version: Version, ReadOnly: s.opts.ReadOnly, TotpRequired: true,
				})
				return
			}
			jsonWrite(w, http.StatusOK, s.issueSession(w, r))
			return
		}
		// token 登录（token 也是忘记认证器时的恢复方式，不再要求 TOTP）
		if s.opts.Token != "" && req.Token != "" {
			if !s.auth.checkToken(req.Token) {
				s.limiter.fail(ip)
				s.audit.log("session.fail_token", ip, r.UserAgent(), nil)
				jsonErr(w, http.StatusUnauthorized, "invalid_token", "token 不正确")
				return
			}
			jsonWrite(w, http.StatusOK, s.issueSession(w, r))
			return
		}
		jsonErr(w, http.StatusUnauthorized, "invalid_credentials", "请提供登录凭据")
	case http.MethodDelete:
		http.SetCookie(w, &http.Cookie{Name: webSessionCookie, Value: "", Path: "/api", MaxAge: -1, HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: webPendingCookie, Value: "", Path: "/api", MaxAge: -1, HttpOnly: true})
		s.audit.log("session.close", s.clientIP(r), r.UserAgent(), nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
	}
}

// handleTOTP 完成密码登录后的二次验证。
func (s *webServer) handleTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	if !s.sameOrigin(r) {
		jsonErr(w, http.StatusForbidden, "csrf", "请求来源不被允许")
		return
	}
	if !s.creds().totpEnabled {
		jsonErr(w, http.StatusBadRequest, "2fa_disabled", "未启用二次验证")
		return
	}
	ip := s.clientIP(r)
	if !s.limiter.allow(ip) {
		s.audit.log("totp.rate_limited", ip, r.UserAgent(), nil)
		jsonErr(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
		return
	}
	c, err := r.Cookie(webPendingCookie)
	if err != nil || !s.pending.check(c.Value) {
		jsonErr(w, http.StatusUnauthorized, "pending_expired", "验证会话已过期，请重新登录")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
		return
	}
	method, ok, reused := s.verifySecondFactor(req.Code)
	if !ok {
		s.pending.fail(c.Value)
		s.limiter.fail(ip)
		if reused {
			s.audit.log("totp.replay", ip, r.UserAgent(), nil)
			jsonErr(w, http.StatusUnauthorized, "code_reused", "动态码已被使用，请等待下一个")
			return
		}
		s.audit.log("totp.fail", ip, r.UserAgent(), nil)
		jsonErr(w, http.StatusUnauthorized, "invalid_code", "动态码或备份码不正确")
		return
	}
	s.pending.consume(c.Value)
	http.SetCookie(w, &http.Cookie{Name: webPendingCookie, Value: "", Path: "/api", MaxAge: -1, HttpOnly: true})
	s.audit.log("totp.ok", ip, r.UserAgent(), map[string]any{"method": method})
	jsonWrite(w, http.StatusOK, s.issueSession(w, r))
}

type taskDTO struct {
	Name         string `json:"name"`
	Src          string `json:"src"`
	Dst          string `json:"dst"`
	Delete       string `json:"delete"`
	RemoveSource string `json:"removeSource"`
	Dangerous    bool   `json:"dangerous"`
	Gate         string `json:"gate"`
	MountPoint   string `json:"mountPoint,omitempty"`
	MountFstype  string `json:"mountFstype,omitempty"`
}

type configDTO struct {
	Path    string `json:"path"`
	Script  string `json:"script"`
	Host    string `json:"host"`
	SSHPort string `json:"sshPort"`
	SSHUser string `json:"sshUser"`
	Policy  string `json:"policy"`
	LogDir  string `json:"logDir"`
}

type stateResp struct {
	Version     string       `json:"version"`
	Config      configDTO    `json:"config"`
	ReadOnly    bool         `json:"readOnly"`
	CSRF        string       `json:"csrf"`
	Tasks       []taskDTO    `json:"tasks"`
	Job         *jobSnapshot `json:"job"`
	LastJob     *jobSnapshot `json:"lastJob"`
	ConfigError string       `json:"configError,omitempty"`
}

func (s *webServer) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	csrf, _ := r.Context().Value(csrfCtxKey{}).(string)
	resp := stateResp{
		Version:  Version,
		ReadOnly: s.opts.ReadOnly,
		CSRF:     csrf,
		Tasks:    []taskDTO{},
		Config:   configDTO{Path: s.configPath, Script: s.scriptPath},
	}
	cfg, err := ParseConfig(s.configPath)
	if err != nil {
		resp.ConfigError = err.Error()
	} else {
		resp.Config = configDTO{
			Path: s.configPath, Script: s.scriptPath,
			Host: cfg.Global.Host, SSHPort: cfg.Global.SSHPort, SSHUser: cfg.Global.SSHUser,
			Policy: cfg.Global.MountPolicy, LogDir: resolveLogDir(cfg.Global.LogDir, s.scriptPath),
		}
		for _, t := range cfg.Tasks {
			resp.Tasks = append(resp.Tasks, taskDTO{
				Name: t.Name, Src: t.Src, Dst: t.Dst,
				Delete: t.Delete, RemoveSource: t.RemoveSource,
				Dangerous:  t.IsDangerous(cfg.Global),
				Gate:       t.MountGateMode(),
				MountPoint: t.MountPoint, MountFstype: t.MountFstype,
			})
		}
	}
	cur, last := s.jobs.snapshots()
	resp.Job = cur
	resp.LastJob = last
	jsonWrite(w, http.StatusOK, resp)
}

type jobsReq struct {
	Tasks        []string `json:"tasks"`
	DryRun       bool     `json:"dryRun"`
	MountCheck   bool     `json:"mountCheck"`
	Confirm      bool     `json:"confirm"`
	PreviewToken string   `json:"previewToken"`
}

func (s *webServer) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	if s.opts.ReadOnly {
		s.audit.log("job.reject_readonly", s.clientIP(r), r.UserAgent(), nil)
		jsonErr(w, http.StatusForbidden, "read_only", "当前为只读模式，不能触发任务")
		return
	}
	var req jobsReq
	if err := decodeJSON(w, r, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
		return
	}
	kind := jobKindRun
	if req.MountCheck {
		kind = jobKindMountCheck
	} else if req.DryRun {
		kind = jobKindDryRun
	}
	cfg, err := ParseConfig(s.configPath)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "config", "读取配置失败: "+err.Error())
		return
	}
	names, verr := resolveTaskNames(cfg, req.Tasks)
	if verr != nil {
		jsonErr(w, http.StatusBadRequest, "unknown_task", verr.Error())
		return
	}
	if kind == jobKindMountCheck && len(names) == 0 {
		for _, t := range cfg.Tasks {
			names = append(names, t.Name)
		}
	}
	if len(names) == 0 {
		jsonErr(w, http.StatusBadRequest, "bad_request", "未选择任务")
		return
	}
	snap, err := s.jobs.start(kind, cfg, names, req.DryRun, req.Confirm, req.PreviewToken)
	switch {
	case err == nil:
	case errors.Is(err, errJobBusy):
		jsonErr(w, http.StatusConflict, "busy", "已有任务在运行")
		return
	case errors.Is(err, errNeedConfirm):
		jsonErr(w, http.StatusForbidden, "confirm_required", "包含危险任务（删除/删除源文件），需要确认")
		return
	case errors.Is(err, errNeedPreview):
		jsonErr(w, http.StatusForbidden, "preview_required", "危险任务需要先执行一次成功的 Dry-run 预览")
		return
	default:
		jsonErr(w, http.StatusInternalServerError, "internal", "启动任务失败")
		return
	}
	s.audit.log("job.start", s.clientIP(r), r.UserAgent(), map[string]any{
		"kind": string(kind), "tasks": names, "dryRun": req.DryRun,
	})
	jsonWrite(w, http.StatusAccepted, map[string]any{"job": snap})
}

func resolveTaskNames(cfg *Config, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	var unknown []string
	for _, n := range names {
		if _, ok := cfg.TaskMap[n]; !ok {
			unknown = append(unknown, n)
			continue
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("未知任务: %s", strings.Join(unknown, ", "))
	}
	return out, nil
}

func (s *webServer) handleCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	cur, last := s.jobs.snapshots()
	jsonWrite(w, http.StatusOK, map[string]any{"job": cur, "lastJob": last})
}

func (s *webServer) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	if s.opts.ReadOnly {
		jsonErr(w, http.StatusForbidden, "read_only", "只读模式")
		return
	}
	snap, ok := s.jobs.cancel()
	if !ok {
		jsonErr(w, http.StatusConflict, "no_job", "当前没有正在运行的任务")
		return
	}
	s.audit.log("job.cancel", s.clientIP(r), r.UserAgent(), map[string]any{"jobId": snap.ID})
	jsonWrite(w, http.StatusOK, map[string]any{"job": snap})
}

func (s *webServer) handleMountCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	if s.opts.ReadOnly {
		jsonErr(w, http.StatusForbidden, "read_only", "只读模式")
		return
	}
	cfg, err := ParseConfig(s.configPath)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "config", "读取配置失败: "+err.Error())
		return
	}
	var names []string
	for _, t := range cfg.Tasks {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		jsonErr(w, http.StatusBadRequest, "bad_request", "配置中没有任务")
		return
	}
	snap, err := s.jobs.start(jobKindMountCheck, cfg, names, false, true, "")
	if errors.Is(err, errJobBusy) {
		jsonErr(w, http.StatusConflict, "busy", "已有任务在运行")
		return
	} else if err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal", "启动挂载检查失败")
		return
	}
	s.audit.log("mountcheck.start", s.clientIP(r), r.UserAgent(), nil)
	jsonWrite(w, http.StatusAccepted, map[string]any{"job": snap})
}

func (s *webServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	s.hub.serve(w, r)
}

func (s *webServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Path
	switch name {
	case "/", "/index.html":
		name = "web/index.html"
	case "/app.js":
		name = "web/app.js"
	case "/style.css":
		name = "web/style.css"
	case "/favicon.ico":
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		http.NotFound(w, r)
		return
	}
	data, err := webAssets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// ---------- 启动 ----------

// runWebCLI 由 main 在 --web 模式下调用，返回进程退出码。
func runWebCLI(scriptPath, configPath string, o webOptions) int {
	if scriptPath == "" {
		fmt.Fprintln(os.Stderr, "错误：找不到 rbackup-cli（Web 模式同样需要）")
		fmt.Fprintln(os.Stderr, "用 -s /path/to/rbackup-cli 或 RBACKUP_SCRIPT 指定")
		return 1
	}
	s, err := newWebServer(o, scriptPath, configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	defer s.close()
	if err := s.serve(); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	return 0
}

func (s *webServer) serve() error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", s.opts.Addr, err)
	}
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	scheme := "http"
	if s.opts.TLSCert != "" {
		scheme = "https"
	}
	host, port, _ := net.SplitHostPort(s.opts.Addr)
	if host == "" {
		host = "0.0.0.0"
	}
	fmt.Fprintf(os.Stderr, "rbackup-web: %s://%s:%s  (配置: %s)\n", scheme, host, port, s.configPath)
	if s.opts.TrustProxy {
		fmt.Fprintln(os.Stderr, "反向代理: 已信任 X-Forwarded-Proto/Host/For（代理须覆盖这些头，勿透传客户端伪造值）")
	}
	if s.opts.PublicURL != "" {
		fmt.Fprintf(os.Stderr, "对外地址: %s\n", s.opts.PublicURL)
	} else if s.opts.TrustProxy {
		fmt.Fprintln(os.Stderr, "对外地址: 按每个请求的 X-Forwarded-Proto/Host 自适应（域名与 IP:端口入口均可）")
	}
	cred := s.creds()
	switch {
	case cred.totpEnabled:
		fmt.Fprintln(os.Stderr, "认证: 密码 + TOTP 二次验证（token 登录可作为恢复方式）")
	case cred.twofaPolicy:
		fmt.Fprintln(os.Stderr, "认证: 密码（2FA 策略已开启但尚未绑定认证器，请在 Web 安全页设置）")
	case cred.passwordHash != "" && s.opts.Token != "":
		fmt.Fprintln(os.Stderr, "认证: token / 用户名密码")
	case cred.passwordHash != "":
		fmt.Fprintln(os.Stderr, "认证: 用户名密码")
	case s.opts.Token != "":
		fmt.Fprintln(os.Stderr, "认证: token")
	default:
		fmt.Fprintln(os.Stderr, "安全提示: 未配置 token/密码，仅建议本机使用；远程访问请加 --web-token-file")
	}
	if s.opts.ReadOnly {
		fmt.Fprintln(os.Stderr, "模式: 只读")
	}

	errCh := make(chan error, 1)
	go func() {
		if s.opts.TLSCert != "" {
			tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
			srv.TLSConfig = tlsCfg
			errCh <- srv.ServeTLS(ln, s.opts.TLSCert, s.opts.TLSKey)
		} else {
			errCh <- srv.Serve(ln)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, shutdownSignals()...)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "收到信号 %v，正在优雅退出…\n", sig)
		s.jobs.shutdown(10 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

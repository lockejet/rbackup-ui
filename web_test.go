package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef"

// writeTestFixture 创建 stub rbackup-cli 与测试配置。
func writeTestFixture(t *testing.T) (script, config, dir string) {
	t.Helper()
	dir = t.TempDir()
	script = filepath.Join(dir, "rbackup-cli")
	body := `#!/bin/bash
cfg=""; task=""; dry=0; mount=0
while [ $# -gt 0 ]; do
  case "$1" in
    --config) cfg="$2"; shift 2 ;;
    --task) task="$2"; shift 2 ;;
    --dry-run) dry=1; shift ;;
    --check-mount) mount=1; shift ;;
    *) shift ;;
  esac
done
if [ "$mount" = 1 ]; then echo "mounted: ok"; exit 0; fi
echo "task=$task dry=$dry config=$cfg"
if [ "$task" = "slow" ]; then sleep 30; fi
echo "[STATS] files=2/3 bytes_total=100 bytes_sent=50 bytes_recv=10 rsync_ms=10 list_ms=5 prep_ms=5"
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(dir, "config.ini")
	cfg := `HOST=example.com
SSH_PORT=22
SSH_USER=admin
SSH_KEY=~/.ssh/id_ed25519
LOG_DIR=` + filepath.Join(dir, "log") + `
GLOBAL_OPTS=-avzhu
RSYNC_PATH=sudo rsync
MOUNT_POLICY=skip

[task_safe]
src=/tmp/a/
dst=/tmp/b

[task_danger]
src=/tmp/c/
dst=/tmp/d
delete=yes

[task_slow]
src=/tmp/e/
dst=/tmp/f
`
	if err := os.WriteFile(config, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return script, config, dir
}

type testEnv struct {
	srv    *webServer
	ts     *httptest.Server
	client *http.Client
	csrf   string
}

func newTestEnv(t *testing.T, mutate func(*webOptions)) *testEnv {
	t.Helper()
	script, config, dir := writeTestFixture(t)
	o := defaultWebOptions()
	o.Token = testToken
	o.Addr = "127.0.0.1:0"
	o.AuditLog = filepath.Join(dir, "audit.log")
	if mutate != nil {
		mutate(&o)
	}
	s, err := newWebServer(o, script, config)
	if err != nil {
		t.Fatalf("newWebServer: %v", err)
	}
	ts := httptest.NewServer(s.handler)
	t.Cleanup(func() { ts.Close(); s.close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{srv: s, ts: ts, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
	if o.Token == "" {
		env.csrf = env.login(t, "", true)
	} else {
		env.csrf = env.login(t, testToken, true)
	}
	return env
}

func (e *testEnv) do(t *testing.T, method, path string, body any, csrf string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set(webCSRFHeader, csrf)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (e *testEnv) login(t *testing.T, token string, wantOK bool) string {
	t.Helper()
	code, data := e.do(t, http.MethodPost, "/api/session", map[string]string{"token": token}, "")
	if wantOK && code != http.StatusOK {
		t.Fatalf("login: code=%d body=%s", code, data)
	}
	var resp sessionResp
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("login: bad json %s: %v", data, err)
	}
	return resp.CSRF
}

// waitLastJob 轮询直到 lastJob 进入期望状态。
func (e *testEnv) waitLastJob(t *testing.T, states ...string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		code, data := e.do(t, http.MethodGet, "/api/jobs/current", nil, "")
		if code == http.StatusOK {
			var resp struct {
				LastJob map[string]any `json:"lastJob"`
			}
			_ = json.Unmarshal(data, &resp)
			if resp.LastJob != nil {
				got, _ := resp.LastJob["state"].(string)
				for _, s := range states {
					if got == s {
						return resp.LastJob
					}
				}
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("等待 job 状态 %v 超时", states)
	return nil
}

func TestWebOptionsValidate(t *testing.T) {
	base := defaultWebOptions()
	base.Addr = "127.0.0.1:30031"
	if err := base.validate(); err != nil {
		t.Fatalf("loopback 默认应通过: %v", err)
	}

	o := defaultWebOptions()
	o.Addr = "0.0.0.0:30031"
	if err := o.validate(); err == nil {
		t.Fatal("非 loopback 无 token 应拒绝")
	}

	o.Token = testToken
	if err := o.validate(); err == nil {
		t.Fatal("非 loopback 无 TLS 应拒绝")
	}
	o.InsecureHTTP = true
	if err := o.validate(); err != nil {
		t.Fatalf("显式 insecure-http 应通过: %v", err)
	}

	o2 := defaultWebOptions()
	o2.TLSCert = "/tmp/a.crt"
	if err := o2.validate(); err == nil {
		t.Fatal("只给 cert 不给 key 应拒绝")
	}

	o3 := defaultWebOptions()
	o3.Addr = "127.0.0.1:30031"
	o3.Token = "short"
	if err := o3.validate(); err == nil {
		t.Fatal("过短 token 应拒绝")
	}
}

func TestWebAuthRequired(t *testing.T) {
	env := newTestEnv(t, nil)

	// 未登录（无 cookie）
	anon := &http.Client{Timeout: 5 * time.Second}
	resp, err := anon.Get(env.ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录 state 应 401，got %d", resp.StatusCode)
	}

	// 错误 token
	code, _ := env.do(t, http.MethodPost, "/api/session", map[string]string{"token": "wrong-token-wrong-token-wrong-token"}, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，got %d", code)
	}

	// 正确状态下可见任务
	code, data := env.do(t, http.MethodGet, "/api/state", nil, "")
	if code != http.StatusOK {
		t.Fatalf("state: %d", code)
	}
	var st stateResp
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Tasks) != 3 {
		t.Fatalf("应读到 3 个任务，got %d", len(st.Tasks))
	}
}

func TestWebCSRF(t *testing.T) {
	env := newTestEnv(t, nil)

	// 缺 CSRF 头
	code, _ := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"safe"}}, "")
	if code != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403，got %d", code)
	}

	// 外站 Origin
	req, _ := http.NewRequest(http.MethodPost, env.ts.URL+"/api/jobs",
		strings.NewReader(`{"tasks":["task_safe"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webCSRFHeader, env.csrf)
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("外站 Origin 应 403，got %d", resp.StatusCode)
	}

	// 正常写操作
	code, data := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"safe"}}, env.csrf)
	if code != http.StatusAccepted {
		t.Fatalf("正常提交应 202，got %d body=%s", code, data)
	}
	env.waitLastJob(t, "done")
}

func TestWebUnknownTask(t *testing.T) {
	env := newTestEnv(t, nil)
	code, data := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"nope"}}, env.csrf)
	if code != http.StatusBadRequest {
		t.Fatalf("未知任务应 400，got %d body=%s", code, data)
	}
	if !strings.Contains(string(data), "unknown_task") {
		t.Fatalf("错误码应为 unknown_task: %s", data)
	}
}

func TestWebDangerRequiresPreview(t *testing.T) {
	env := newTestEnv(t, nil)

	// 直接跑危险任务：需要确认
	code, data := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"danger"}}, env.csrf)
	if code != http.StatusForbidden || !strings.Contains(string(data), "confirm_required") {
		t.Fatalf("应 confirm_required，got %d %s", code, data)
	}

	// 带 confirm 但没有预览：需要预览
	code, data = env.do(t, http.MethodPost, "/api/jobs",
		map[string]any{"tasks": []string{"danger"}, "confirm": true}, env.csrf)
	if code != http.StatusForbidden || !strings.Contains(string(data), "preview_required") {
		t.Fatalf("应 preview_required，got %d %s", code, data)
	}

	// Dry-run 预览
	code, _ = env.do(t, http.MethodPost, "/api/jobs",
		map[string]any{"tasks": []string{"danger"}, "dryRun": true}, env.csrf)
	if code != http.StatusAccepted {
		t.Fatalf("dryrun 应 202，got %d", code)
	}
	last := env.waitLastJob(t, "done")
	token, _ := last["previewToken"].(string)
	if token == "" {
		t.Fatal("dry-run 成功后应返回 previewToken")
	}

	// 带预览 token 执行
	code, data = env.do(t, http.MethodPost, "/api/jobs",
		map[string]any{"tasks": []string{"danger"}, "confirm": true, "previewToken": token}, env.csrf)
	if code != http.StatusAccepted {
		t.Fatalf("带预览执行应 202，got %d %s", code, data)
	}
	env.waitLastJob(t, "done")
}

func TestWebBusyAndCancel(t *testing.T) {
	env := newTestEnv(t, nil)

	code, _ := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"slow"}}, env.csrf)
	if code != http.StatusAccepted {
		t.Fatalf("slow job 应 202，got %d", code)
	}
	// 运行中再提交 → 409
	code, data := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"safe"}}, env.csrf)
	if code != http.StatusConflict || !strings.Contains(string(data), "busy") {
		t.Fatalf("应 busy 409，got %d %s", code, data)
	}

	// 取消
	code, _ = env.do(t, http.MethodPost, "/api/jobs/current/cancel", nil, env.csrf)
	if code != http.StatusOK {
		t.Fatalf("cancel 应 200，got %d", code)
	}
	env.waitLastJob(t, "canceled")
}

func TestWebReadOnly(t *testing.T) {
	env := newTestEnv(t, func(o *webOptions) { o.ReadOnly = true })
	code, data := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"safe"}}, env.csrf)
	if code != http.StatusForbidden || !strings.Contains(string(data), "read_only") {
		t.Fatalf("只读模式应 403 read_only，got %d %s", code, data)
	}
}

func TestWebTokenFreeLoopback(t *testing.T) {
	env := newTestEnv(t, func(o *webOptions) { o.Token = "" })
	if env.csrf == "" {
		t.Fatal("token-free 模式也应返回 csrf")
	}
	code, _ := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"safe"}}, env.csrf)
	if code != http.StatusAccepted {
		t.Fatalf("token-free 提交应 202，got %d", code)
	}
}

func TestWebHealthAndStatic(t *testing.T) {
	env := newTestEnv(t, nil)
	code, data := env.do(t, http.MethodGet, "/health", nil, "")
	if code != http.StatusOK || !strings.Contains(string(data), `"status":"ok"`) {
		t.Fatalf("health 异常: %d %s", code, data)
	}
	for _, p := range []string{"/", "/app.js", "/style.css"} {
		code, data := env.do(t, http.MethodGet, p, nil, "")
		if code != http.StatusOK || len(data) == 0 {
			t.Fatalf("静态资源 %s 异常: %d", p, code)
		}
	}
}

// TestWebSSE 校验 SSE 至少收到 log 与 job_state 事件。
func TestWebSSE(t *testing.T) {
	env := newTestEnv(t, nil)

	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE Content-Type 异常: %s", ct)
	}

	lines := make(chan string, 512)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	// 等连接建立后再触发 job
	time.Sleep(50 * time.Millisecond)
	code, data := env.do(t, http.MethodPost, "/api/jobs", map[string]any{"tasks": []string{"safe"}}, env.csrf)
	if code != http.StatusAccepted {
		t.Fatalf("job 提交失败: %d %s", code, data)
	}

	var gotLog, gotJobState, gotTaskResult bool
	deadline := time.After(10 * time.Second)
	for !(gotLog && gotJobState && gotTaskResult) {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("SSE 流提前结束")
			}
			switch {
			case strings.HasPrefix(line, "event: log"):
				gotLog = true
			case strings.HasPrefix(line, "event: task_result"):
				gotTaskResult = true
			case strings.HasPrefix(line, "event: job_state"):
				gotJobState = true
			}
		case <-deadline:
			t.Fatalf("SSE 超时（log=%v task_result=%v job_state=%v）", gotLog, gotTaskResult, gotJobState)
		}
	}
	_ = resp.Body.Close()
	wg.Wait()
}

func TestWebMountCheck(t *testing.T) {
	env := newTestEnv(t, nil)
	code, data := env.do(t, http.MethodPost, "/api/mountcheck", nil, env.csrf)
	if code != http.StatusAccepted {
		t.Fatalf("mountcheck 应 202，got %d %s", code, data)
	}
	last := env.waitLastJob(t, "done", "failed")
	if last["kind"] != "mountcheck" {
		t.Fatalf("kind 应为 mountcheck: %v", last["kind"])
	}
}

func TestWebDecodeStrict(t *testing.T) {
	env := newTestEnv(t, nil)
	code, _ := env.do(t, http.MethodPost, "/api/jobs",
		map[string]any{"tasks": []string{"safe"}, "unknownField": 1}, env.csrf)
	if code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，got %d", code)
	}
	// 确认没有把 job 启起来
	cur, _ := env.srv.jobs.snapshots()
	if cur != nil {
		t.Fatalf("非法请求不应启动 job: %v", cur)
	}
}

func TestParseCLIWebFlags(t *testing.T) {
	oldMode, oldOpts := webMode, webOpts
	defer func() { webMode, webOpts = oldMode, oldOpts }()

	reset := func() { webMode = false; webOpts = defaultWebOptions() }

	// 只给 --web-token-file（没有 --web）也应自动进入 Web 模式
	reset()
	if _, _, err := parseCLI([]string{"rbackup-ui", "--web-token-file", "/tmp/web.token", "--web-addr", "127.0.0.1:9999"}); err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if !webMode {
		t.Fatal("出现 --web-* 选项时应自动启用 Web 模式")
	}
	if webOpts.TokenFile != "/tmp/web.token" || webOpts.Addr != "127.0.0.1:9999" {
		t.Fatalf("web 选项解析错误: %+v", webOpts)
	}

	// 缺值必须报错（而不是静默忽略）
	reset()
	if _, _, err := parseCLI([]string{"rbackup-ui", "--web-token-file"}); err == nil {
		t.Fatal("--web-token-file 缺值应报错")
	}
	reset()
	if _, _, err := parseCLI([]string{"rbackup-ui", "--web-addr"}); err == nil {
		t.Fatal("--web-addr 缺值应报错")
	}

	// -c/-s 缺值同样报错
	reset()
	if _, _, err := parseCLI([]string{"rbackup-ui", "-c"}); err == nil {
		t.Fatal("-c 缺值应报错")
	}

	// 仅 --web 仍可用
	reset()
	if _, _, err := parseCLI([]string{"rbackup-ui", "--web"}); err != nil || !webMode {
		t.Fatalf("--web 解析失败: err=%v mode=%v", err, webMode)
	}

	// 非 web 参数不应误启用
	reset()
	if _, _, err := parseCLI([]string{"rbackup-ui", "-c", "/tmp/x.ini"}); err != nil || webMode {
		t.Fatalf("普通参数不应启用 web: err=%v mode=%v", err, webMode)
	}
}

// ---------- A+B+C：密码登录 / TOTP / 认证信息 ----------

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if !validPasswordHash(h) {
		t.Fatalf("哈希格式应合法: %s", h)
	}
	if !verifyPassword(h, "secret123") {
		t.Fatal("正确密码应通过")
	}
	if verifyPassword(h, "wrong") {
		t.Fatal("错误密码不应通过")
	}
	if verifyPassword("bad-format", "x") || validPasswordHash("bad-format") {
		t.Fatal("非法哈希格式应拒绝")
	}
}

func TestTOTPCode(t *testing.T) {
	secret, err := decodeTOTPSecret("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	if err != nil {
		t.Fatal(err)
	}
	// RFC 6238 测试向量（SHA1 密钥 "12345678901234567890"，T=59 → 8 位 94287082）
	if got := totpCode(secret, time.Unix(59, 0)); got != "287082" {
		t.Fatalf("TOTP 计算错误: got %s want 287082", got)
	}
	if _, ok := verifyTOTP(secret, "000000"); ok {
		t.Fatal("错误动态码不应通过")
	}
	if _, ok := verifyTOTP(secret, totpCode(secret, time.Now())); !ok {
		t.Fatal("当前动态码应通过")
	}
}

func newTestEnvAuth(t *testing.T, o webOptions) *testEnv {
	t.Helper()
	script, config, dir := writeTestFixture(t)
	o.AuditLog = filepath.Join(dir, "audit.log")
	s, err := newWebServer(o, script, config)
	if err != nil {
		t.Fatalf("newWebServer: %v", err)
	}
	ts := httptest.NewServer(s.handler)
	t.Cleanup(func() { ts.Close(); s.close() })
	jar, _ := cookiejar.New(nil)
	return &testEnv{srv: s, ts: ts, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
}

func (e *testEnv) postSession(t *testing.T, body any) (int, sessionResp) {
	t.Helper()
	code, data := e.do(t, http.MethodPost, "/api/session", body, "")
	var r sessionResp
	_ = json.Unmarshal(data, &r)
	return code, r
}

func TestWebAuthInfo(t *testing.T) {
	env := newTestEnv(t, nil)
	code, data := env.do(t, http.MethodGet, "/api/authinfo", nil, "")
	if code != http.StatusOK {
		t.Fatalf("authinfo: %d", code)
	}
	var info authInfoResp
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Methods) != 1 || info.Methods[0] != "token" {
		t.Fatalf("方法应为 [token]: %v", info.Methods)
	}
	if info.TOTP {
		t.Fatal("默认不应启用 TOTP")
	}
}

func TestWebPasswordLogin(t *testing.T) {
	hash, err := hashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	o := webOptions{Addr: "127.0.0.1:0", User: "alice", PasswordHash: hash, TwoFA: "off",
		MaxClients: 8, SessionTTL: time.Hour}
	env := newTestEnvAuth(t, o)

	// 错误密码
	code, _ := env.postSession(t, map[string]string{"username": "alice", "password": "bad"})
	if code != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401，got %d", code)
	}
	// 错误用户名
	code, _ = env.postSession(t, map[string]string{"username": "bob", "password": "secret123"})
	if code != http.StatusUnauthorized {
		t.Fatalf("错误用户名应 401，got %d", code)
	}
	// 正确
	code, r := env.postSession(t, map[string]string{"username": "alice", "password": "secret123"})
	if code != http.StatusOK || r.CSRF == "" || r.TotpRequired {
		t.Fatalf("正确密码应登录成功: code=%d resp=%+v", code, r)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/state", nil, ""); code != http.StatusOK {
		t.Fatalf("登录后 state 应 200，got %d", code)
	}
	// authinfo 应显示 password
	_, data := env.do(t, http.MethodGet, "/api/authinfo", nil, "")
	var info authInfoResp
	_ = json.Unmarshal(data, &info)
	found := false
	for _, m := range info.Methods {
		if m == "password" {
			found = true
		}
	}
	if !found {
		t.Fatalf("authinfo 应包含 password: %v", info.Methods)
	}
}

func TestWebTOTPFlow(t *testing.T) {
	hash, err := hashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	secretStr, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "totp.secret")
	if err := os.WriteFile(secretFile, []byte(secretStr), 0o600); err != nil {
		t.Fatal(err)
	}
	o := webOptions{Addr: "127.0.0.1:0", User: "alice", PasswordHash: hash,
		TwoFA: "totp", TOTPSecretFile: secretFile, MaxClients: 8, SessionTTL: time.Hour}
	env := newTestEnvAuth(t, o)

	secret, err := decodeTOTPSecret(secretStr)
	if err != nil {
		t.Fatal(err)
	}

	// 密码正确 → 返回 totpRequired，且此时还没有会话
	code, r := env.postSession(t, map[string]string{"username": "alice", "password": "secret123"})
	if code != http.StatusOK || !r.TotpRequired || r.CSRF != "" {
		t.Fatalf("应先要求 2FA: code=%d resp=%+v", code, r)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/state", nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("2FA 未完成前 state 应 401，got %d", code)
	}

	// 错误动态码
	code, _ = env.do(t, http.MethodPost, "/api/session/totp", map[string]string{"code": "000000"}, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("错误动态码应 401，got %d", code)
	}

	// 正确动态码
	code, data := env.do(t, http.MethodPost, "/api/session/totp",
		map[string]string{"code": totpCode(secret, time.Now())}, "")
	if code != http.StatusOK {
		t.Fatalf("正确动态码应 200，got %d %s", code, data)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/state", nil, ""); code != http.StatusOK {
		t.Fatalf("2FA 完成后 state 应 200，got %d", code)
	}
}

func TestWebTokenSkipsTOTP(t *testing.T) {
	hash, err := hashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	secretStr, _ := newTOTPSecret()
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "totp.secret")
	_ = os.WriteFile(secretFile, []byte(secretStr), 0o600)
	o := webOptions{Addr: "127.0.0.1:0", Token: testToken, User: "alice", PasswordHash: hash,
		TwoFA: "totp", TOTPSecretFile: secretFile, MaxClients: 8, SessionTTL: time.Hour}
	env := newTestEnvAuth(t, o)

	// token 登录不要求 TOTP（token 作为恢复方式）
	code, r := env.postSession(t, map[string]string{"token": testToken})
	if code != http.StatusOK || r.TotpRequired || r.CSRF == "" {
		t.Fatalf("token 登录应直接成功且不要求 2FA: code=%d resp=%+v", code, r)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/state", nil, ""); code != http.StatusOK {
		t.Fatalf("token 登录后 state 应 200，got %d", code)
	}
}

func TestParseCLIAuthFlags(t *testing.T) {
	oldMode, oldOpts := webMode, webOpts
	defer func() { webMode, webOpts = oldMode, oldOpts }()
	webMode = false
	webOpts = defaultWebOptions()

	if _, _, err := parseCLI([]string{"rbackup-ui",
		"--web-user", "alice",
		"--web-password-hash-file", "/tmp/pw",
		"--web-2fa", "totp",
		"--web-totp-secret-file", "/tmp/totp"}); err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if !webMode || webOpts.User != "alice" || webOpts.PasswordHashFile != "/tmp/pw" ||
		webOpts.TwoFA != "totp" || webOpts.TOTPSecretFile != "/tmp/totp" {
		t.Fatalf("认证相关 flag 解析错误: %+v", webOpts)
	}

	// 缺值必须报错
	webMode = false
	webOpts = defaultWebOptions()
	if _, _, err := parseCLI([]string{"rbackup-ui", "--web-2fa"}); err == nil {
		t.Fatal("--web-2fa 缺值应报错")
	}
}

func TestWebTokenWhitespaceTolerant(t *testing.T) {
	env := newTestEnv(t, nil)
	// 模拟从终端复制时带上首尾空白/换行
	code, r := env.postSession(t, map[string]string{"token": "  " + testToken + "\n"})
	if code != http.StatusOK || r.CSRF == "" {
		t.Fatalf("带首尾空白的 token 应被接受: code=%d resp=%+v", code, r)
	}
}

// TestRenderQRTerminal 验证终端二维码渲染（半块字符、尺寸、静区）。
func TestRenderQRTerminal(t *testing.T) {
	uri := otpauthURI("rbackup-ui", "admin", "JBSWY3DPEHPK3PXP")
	var buf strings.Builder
	if err := renderQRTerminal(&buf, uri); err != nil {
		t.Fatalf("renderQRTerminal: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "▀") {
		t.Fatal("输出应包含半块字符")
	}
	plain := out
	// 去掉 ANSI 颜色码后统计行数与宽度
	plain = stripANSI(plain)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) < 20 {
		t.Fatalf("二维码行数过少: %d", len(lines))
	}
	width := len([]rune(lines[0]))
	if width < 20 || width != len([]rune(lines[len(lines)-1])) {
		t.Fatalf("二维码宽度异常: %d", width)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		if r == '\x1b' {
			skip = true
			continue
		}
		if skip {
			if r == 'm' {
				skip = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestOTPAuthURI(t *testing.T) {
	uri := otpauthURI("rbackup-ui", "admin", "ABC234")
	if !strings.HasPrefix(uri, "otpauth://totp/") || !strings.Contains(uri, "secret=ABC234") ||
		!strings.Contains(uri, "issuer=rbackup-ui") {
		t.Fatalf("otpauth URI 异常: %s", uri)
	}
}

func TestParseCLIAuditLogDash(t *testing.T) {
	oldMode, oldOpts := webMode, webOpts
	defer func() { webMode, webOpts = oldMode, oldOpts }()
	webMode = false
	webOpts = defaultWebOptions()
	if _, _, err := parseCLI([]string{"rbackup-ui", "--web-audit-log", "-"}); err != nil {
		t.Fatalf("--web-audit-log - 应合法: %v", err)
	}
	if webOpts.AuditLog != "-" {
		t.Fatalf("AuditLog 应为 -: %q", webOpts.AuditLog)
	}
}

// ---------- Web 安全中心：2FA 自助管理 / 手机登录 / 二维码 ----------

func totpAt(secretStr string, at time.Time) string {
	secret, err := decodeTOTPSecret(secretStr)
	if err != nil {
		return ""
	}
	return totpCode(secret, at)
}

func totpNow(secretStr string) string { return totpAt(secretStr, time.Now()) }

func newPasswordEnv(t *testing.T, twofa, secretFile string) *testEnv {
	t.Helper()
	// 隔离 HOME/XDG：默认 secret 路径会落到 ~/.config/rbackup-ui/web.totp
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	hash, err := hashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	o := webOptions{Addr: "127.0.0.1:0", User: "alice", PasswordHash: hash, TwoFA: twofa,
		MaxClients: 8, SessionTTL: time.Hour}
	if secretFile != "" {
		o.TOTPSecretFile = secretFile
	}
	return newTestEnvAuth(t, o)
}

func TestWeb2FASelfSetup(t *testing.T) {
	env := newPasswordEnv(t, "totp", "") // 初始没有 2FA：策略开启，未绑定

	// 未绑定认证器时密码登录不应要求 2FA
	code, r := env.postSession(t, map[string]string{"username": "alice", "password": "secret123"})
	if code != http.StatusOK || r.TotpRequired || r.CSRF == "" {
		t.Fatalf("未绑定时应直接登录: code=%d resp=%+v", code, r)
	}
	csrf := r.CSRF

	code, data := env.do(t, http.MethodGet, "/api/2fa", nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/2fa: %d", code)
	}
	var st twofaStatusResp
	_ = json.Unmarshal(data, &st)
	if !st.Policy || st.Configured {
		t.Fatalf("应为 policy=true configured=false: %+v", st)
	}

	code, data = env.do(t, http.MethodPost, "/api/2fa/setup", map[string]string{}, csrf)
	if code != http.StatusOK {
		t.Fatalf("setup: %d %s", code, data)
	}
	var setupResp struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(data, &setupResp); err != nil || setupResp.Secret == "" || setupResp.URI == "" {
		t.Fatalf("setup 响应异常: %s", data)
	}
	if !strings.HasPrefix(setupResp.URI, "otpauth://totp/") {
		t.Fatalf("otpauth URI 异常: %s", setupResp.URI)
	}

	// 错误码 → 401
	code, _ = env.do(t, http.MethodPost, "/api/2fa/setup/confirm",
		map[string]string{"secret": setupResp.Secret, "code": "000000"}, csrf)
	if code != http.StatusUnauthorized {
		t.Fatalf("错误码应 401，got %d", code)
	}

	// 正确码 → 绑定成功 + 10 个备份码
	code, data = env.do(t, http.MethodPost, "/api/2fa/setup/confirm",
		map[string]string{"secret": setupResp.Secret, "code": totpNow(setupResp.Secret)}, csrf)
	if code != http.StatusOK {
		t.Fatalf("confirm: %d %s", code, data)
	}
	var confirmResp struct {
		BackupCodes []string `json:"backupCodes"`
	}
	_ = json.Unmarshal(data, &confirmResp)
	if len(confirmResp.BackupCodes) != backupCodeCount {
		t.Fatalf("应返回 %d 个备份码: %v", backupCodeCount, confirmResp.BackupCodes)
	}

	_, data = env.do(t, http.MethodGet, "/api/2fa", nil, "")
	_ = json.Unmarshal(data, &st)
	if !st.Configured || st.BackupCodes != backupCodeCount {
		t.Fatalf("绑定后状态异常: %+v", st)
	}

	// 新会话登录：需要 2FA，用备份码通过
	env2 := newPasswordEnv(t, "totp", "")
	_ = env2 // 仅确保 helper 可用；下面复用同一 env（会话与凭据共享服务端）
	code, r = env.postSession(t, map[string]string{"username": "alice", "password": "secret123"})
	if code != http.StatusOK || !r.TotpRequired {
		t.Fatalf("绑定后应要求 2FA: code=%d resp=%+v", code, r)
	}
	code, data = env.do(t, http.MethodPost, "/api/session/totp",
		map[string]string{"code": confirmResp.BackupCodes[0]}, "")
	if code != http.StatusOK {
		t.Fatalf("备份码登录应成功: %d %s", code, data)
	}
	_, data = env.do(t, http.MethodGet, "/api/2fa", nil, "")
	_ = json.Unmarshal(data, &st)
	if st.BackupCodes != backupCodeCount-1 {
		t.Fatalf("备份码应被消耗 1 个，剩余 %d", st.BackupCodes)
	}
}

func TestWeb2FABackupRegenAndDisable(t *testing.T) {
	secretStr, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "web.totp")
	if err := os.WriteFile(secretFile, []byte(secretStr), 0o600); err != nil {
		t.Fatal(err)
	}
	env := newPasswordEnv(t, "totp", secretFile)

	code, r := env.postSession(t, map[string]string{"username": "alice", "password": "secret123"})
	if code != http.StatusOK || !r.TotpRequired {
		t.Fatalf("应要求 2FA: %d %+v", code, r)
	}
	code, data := env.do(t, http.MethodPost, "/api/session/totp", map[string]string{"code": totpNow(secretStr)}, "")
	if code != http.StatusOK {
		t.Fatalf("2FA 登录失败: %d %s", code, data)
	}
	var sr sessionResp
	_ = json.Unmarshal(data, &sr)
	csrf := sr.CSRF

	// 用下一个时间窗的码重新生成备份码（避免重放）
	next := totpAt(secretStr, time.Now().Add(30*time.Second))
	code, data = env.do(t, http.MethodPost, "/api/2fa/backup-codes", map[string]string{"code": next}, csrf)
	if code != http.StatusOK {
		t.Fatalf("重新生成备份码失败: %d %s", code, data)
	}
	var regen struct {
		BackupCodes []string `json:"backupCodes"`
	}
	_ = json.Unmarshal(data, &regen)
	if len(regen.BackupCodes) != backupCodeCount {
		t.Fatalf("备份码数量异常: %d", len(regen.BackupCodes))
	}

	// 关闭 2FA：用刚生成的备份码（同一 30s 窗口内动态码会触发防重放）
	code, data = env.do(t, http.MethodPost, "/api/2fa/disable", map[string]string{"code": regen.BackupCodes[0]}, csrf)
	if code != http.StatusOK {
		t.Fatalf("关闭 2FA 失败: %d %s", code, data)
	}
	_, data = env.do(t, http.MethodGet, "/api/2fa", nil, "")
	var st twofaStatusResp
	_ = json.Unmarshal(data, &st)
	if !st.Policy || st.Configured {
		t.Fatalf("关闭后应为 policy=true configured=false: %+v", st)
	}
	// 关闭后密码登录不再需要 2FA
	code, r = env.postSession(t, map[string]string{"username": "alice", "password": "secret123"})
	if code != http.StatusOK || r.TotpRequired {
		t.Fatalf("关闭后应直接登录: code=%d resp=%+v", code, r)
	}
}

func TestWebPhoneLogin(t *testing.T) {
	env := newTestEnv(t, nil) // token 模式，newTestEnv 已登录
	code, data := env.do(t, http.MethodPost, "/api/phone-login", map[string]string{}, env.csrf)
	if code != http.StatusOK {
		t.Fatalf("创建手机登录链接失败: %d %s", code, data)
	}
	var link struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expiresIn"`
	}
	_ = json.Unmarshal(data, &link)
	if !strings.Contains(link.URL, "#t=") || link.ExpiresIn <= 0 {
		t.Fatalf("链接响应异常: %s", data)
	}
	tok := link.URL[strings.Index(link.URL, "#t=")+3:]

	// 新客户端（模拟手机）用一次性 token 换会话
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	req, _ := http.NewRequest(http.MethodPost, env.ts.URL+"/api/session/phone",
		strings.NewReader(`{"token":"`+tok+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("手机登录兑换失败: %d", res.StatusCode)
	}
	// 同一 token 不能再次使用
	req2, _ := http.NewRequest(http.MethodPost, env.ts.URL+"/api/session/phone",
		strings.NewReader(`{"token":"`+tok+`"}`))
	req2.Header.Set("Content-Type", "application/json")
	res2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("一次性链接应失效，got %d", res2.StatusCode)
	}
}

func TestWebQREndpoint(t *testing.T) {
	env := newTestEnv(t, nil)
	code, data := env.do(t, http.MethodGet, "/api/qr.png?data=hello", nil, "")
	if code != http.StatusOK || len(data) < 100 {
		t.Fatalf("二维码接口异常: %d len=%d", code, len(data))
	}
	// 未登录不可访问
	jar, _ := cookiejar.New(nil)
	res, err := (&http.Client{Jar: jar, Timeout: 10 * time.Second}).Get(env.ts.URL + "/api/qr.png?data=hello")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录访问二维码应 401，got %d", res.StatusCode)
	}
}

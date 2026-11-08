package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newProxyServer 构造一个启用/不启用 trust-proxy 的测试服务器（不启动 HTTP 服务）。
func newProxyServer(t *testing.T, mutate func(*webOptions)) *webServer {
	t.Helper()
	script, config, dir := writeTestFixture(t)
	o := defaultWebOptions()
	o.Token = testToken
	o.Addr = "127.0.0.1:0"
	o.AuditLog = filepath.Join(dir, "audit.log")
	o.SessionTTL = time.Hour
	if mutate != nil {
		mutate(&o)
	}
	s, err := newWebServer(o, script, config)
	if err != nil {
		t.Fatalf("newWebServer: %v", err)
	}
	t.Cleanup(s.close)
	return s
}

func newReq(t *testing.T, target string, hdr map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, nil)
	r.RemoteAddr = "127.0.0.1:54321"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIPTrustProxy(t *testing.T) {
	trusted := newProxyServer(t, func(o *webOptions) { o.TrustProxy = true })
	r := newReq(t, "http://127.0.0.1/api/x", map[string]string{
		"X-Forwarded-For": "203.0.113.9, 10.0.0.1",
	})
	if got := trusted.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("XFF 应取最左值: %q", got)
	}
	r2 := newReq(t, "http://127.0.0.1/api/x", map[string]string{"X-Real-IP": "198.51.100.7"})
	if got := trusted.clientIP(r2); got != "198.51.100.7" {
		t.Fatalf("X-Real-IP 兜底: %q", got)
	}
	r3 := newReq(t, "http://127.0.0.1/api/x", nil)
	if got := trusted.clientIP(r3); got != "127.0.0.1" {
		t.Fatalf("RemoteAddr 兜底: %q", got)
	}

	untrusted := newProxyServer(t, nil)
	r4 := newReq(t, "http://127.0.0.1/api/x", map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if got := untrusted.clientIP(r4); got != "127.0.0.1" {
		t.Fatalf("未信任代理时必须忽略 XFF: %q", got)
	}
}

func TestProxySchemeHostAndSecure(t *testing.T) {
	s := newProxyServer(t, func(o *webOptions) { o.TrustProxy = true })
	r := newReq(t, "http://127.0.0.1/api/x", map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "backup.example.com",
	})
	if got := s.requestScheme(r); got != "https" {
		t.Fatalf("scheme: %q", got)
	}
	if got := s.effectiveHost(r); got != "backup.example.com" {
		t.Fatalf("host: %q", got)
	}
	if got := s.publicBase(r); got != "https://backup.example.com" {
		t.Fatalf("base: %q", got)
	}
	if !s.requestIsSecure(r) {
		t.Fatal("XFP=https 应判定为安全连接")
	}

	untrusted := newProxyServer(t, nil)
	if got := untrusted.requestScheme(r); got != "http" {
		t.Fatalf("未信任代理时必须忽略 XFP: %q", got)
	}
	if untrusted.requestIsSecure(r) {
		t.Fatal("未信任代理时明文连接不应判定为 Secure")
	}
}

func TestPublicURLOverridesProxy(t *testing.T) {
	s := newProxyServer(t, func(o *webOptions) {
		o.TrustProxy = true
		o.PublicURL = "https://backup.example.com/"
	})
	if s.opts.PublicURL != "https://backup.example.com" {
		t.Fatalf("PublicURL 应去掉尾斜杠: %q", s.opts.PublicURL)
	}
	r := newReq(t, "http://127.0.0.1/api/x", map[string]string{"X-Forwarded-Host": "other.example.com"})
	if got := s.publicBase(r); got != "https://backup.example.com" {
		t.Fatalf("publicBase 应优先 PublicURL: %q", got)
	}
	if !s.requestIsSecure(r) {
		t.Fatal("PublicURL 为 https 时应判定为安全连接")
	}
}

func TestSameOriginWithProxy(t *testing.T) {
	s := newProxyServer(t, func(o *webOptions) { o.TrustProxy = true })
	ok := newReq(t, "http://127.0.0.1/api/x", map[string]string{
		"Origin":            "https://backup.example.com",
		"X-Forwarded-Host":  "backup.example.com",
		"X-Forwarded-Proto": "https",
	})
	if !s.sameOrigin(ok) {
		t.Fatal("XFH 对应的 Origin 应通过同源校验")
	}
	bad := newReq(t, "http://127.0.0.1/api/x", map[string]string{
		"Origin":           "https://evil.example.com",
		"X-Forwarded-Host": "backup.example.com",
	})
	if s.sameOrigin(bad) {
		t.Fatal("其他 Origin 应被拒绝")
	}

	// 未信任代理时，Origin 只能与直连 Host 比较
	untrusted := newProxyServer(t, nil)
	u := newReq(t, "http://127.0.0.1/api/x", map[string]string{
		"Origin":           "https://backup.example.com",
		"X-Forwarded-Host": "backup.example.com",
	})
	if untrusted.sameOrigin(u) {
		t.Fatal("未信任代理时伪造 XFH 不应通过同源校验")
	}
}

func TestSessionCookieSecureBehindProxy(t *testing.T) {
	s := newProxyServer(t, func(o *webOptions) { o.TrustProxy = true })
	r := newReq(t, "http://127.0.0.1/api/session", map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "backup.example.com",
	})
	w := httptest.NewRecorder()
	s.issueSession(w, r)
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == webSessionCookie {
			found = true
			if !c.Secure {
				t.Fatal("反代 HTTPS 入口生成的会话 cookie 必须带 Secure")
			}
		}
	}
	if !found {
		t.Fatal("未生成会话 cookie")
	}

	// 直连明文时不应带 Secure（否则浏览器不会回传）
	untrusted := newProxyServer(t, nil)
	w2 := httptest.NewRecorder()
	untrusted.issueSession(w2, r)
	for _, c := range w2.Result().Cookies() {
		if c.Name == webSessionCookie && c.Secure {
			t.Fatal("直连明文时 cookie 不应带 Secure")
		}
	}
}

func TestPhoneLinkUsesProxyHost(t *testing.T) {
	s := newProxyServer(t, func(o *webOptions) { o.TrustProxy = true })
	r := newReq(t, "http://127.0.0.1/api/phone-login", map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "backup.example.com",
	})
	w := httptest.NewRecorder()
	s.handlePhoneLink(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("phone-login: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.URL, "https://backup.example.com/#t=") {
		t.Fatalf("扫码链接域名错误: %q", resp.URL)
	}

	// 配置 PublicURL 时固定使用它
	s2 := newProxyServer(t, func(o *webOptions) {
		o.TrustProxy = true
		o.PublicURL = "https://fixed.example.com"
	})
	w2 := httptest.NewRecorder()
	s2.handlePhoneLink(w2, newReq(t, "http://127.0.0.1/api/phone-login", map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "backup.example.com",
	}))
	var resp2 struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if !strings.HasPrefix(resp2.URL, "https://fixed.example.com/#t=") {
		t.Fatalf("PublicURL 生效失败: %q", resp2.URL)
	}
}

func TestDetectMode(t *testing.T) {
	cases := map[string]string{
		"rbackup-tui":                    "tui",
		"rbackup-web":                    "web",
		"/home/u/.local/bin/rbackup-web": "web",
		"rbackup-web.exe":                "web",
		"RBackup-WEB":                    "web",
		"rbackup-ui":                     "tui",
		"unknown":                        "tui",
	}
	for name, want := range cases {
		if got := detectMode(name); got != want {
			t.Errorf("detectMode(%q)=%q want %q", name, got, want)
		}
	}
}

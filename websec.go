package main

// Web 安全中心后端：
//   - 2FA 自助管理：查看状态、设置（生成 secret + 二维码 + 校验动态码）、
//     重新生成备份码、关闭 2FA；
//   - 手机扫码登录：生成一次性登录链接（TTL 120s、单次使用），手机扫码后直接建立会话；
//   - 服务端二维码 PNG 渲染（登录页/安全页的 QR 图片由 /api/qr.png 提供）。
//
// 所有写操作都要求：已登录会话 + CSRF 头 + 同源；敏感操作（关闭/重生成备份码）要求当前动态码或备份码。

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rsc.io/qr"
)

const (
	phoneLinkTTL       = 120 * time.Second
	phoneLinkMax       = 8
	backupCodeCount    = 10
	backupCodeLen      = 10
	backupCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	qrMaxDataLen       = 1024
)

// ---------- 凭据状态（可在运行中由 2FA 管理接口更新） ----------

type credState struct {
	user         string
	passwordHash string
	twofaPolicy  bool
	totpSecret   []byte
	totpEnabled  bool // 策略开启 且 已配置 secret
	backupHashes []string
	secretFile   string
	backupFile   string
}

func (c credState) clone() credState {
	out := c
	out.totpSecret = append([]byte(nil), c.totpSecret...)
	out.backupHashes = append([]string(nil), c.backupHashes...)
	return out
}

func (s *webServer) creds() credState {
	s.credMu.RLock()
	defer s.credMu.RUnlock()
	return s.cred.clone()
}

// updateCred 在写锁下修改凭据；回调内不要做 IO 以外的阻塞操作。
func (s *webServer) updateCred(fn func(*credState)) {
	s.credMu.Lock()
	defer s.credMu.Unlock()
	fn(&s.cred)
}

// ---------- 备份码 ----------

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeBackupCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "-", "", "_", "", "\t", "").Replace(s)
}

func hashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeBackupCode(code)))
	return hex.EncodeToString(sum[:])
}

// newBackupCodes 生成 n 个明文码与对应哈希（明文只返回给前端展示一次）。
func newBackupCodes(n int) ([]string, []string) {
	plain := make([]string, 0, n)
	hashes := make([]string, 0, n)
	seen := map[string]bool{}
	for len(plain) < n {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			continue
		}
		code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:backupCodeLen]
		if seen[code] {
			continue
		}
		seen[code] = true
		plain = append(plain, code)
		hashes = append(hashes, hashBackupCode(code))
	}
	return plain, hashes
}

func formatBackupCode(code string) string {
	if len(code) != backupCodeLen {
		return code
	}
	return code[:5] + "-" + code[5:]
}

func loadBackupHashes(path string) []string {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 64 && isHex(line) {
			out = append(out, strings.ToLower(line))
		}
	}
	return out
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func saveBackupHashes(path string, hashes []string) error {
	var b strings.Builder
	for _, h := range hashes {
		b.WriteString(h)
		b.WriteString("\n")
	}
	return writeFileAtomic(path, []byte(b.String()), 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------- 第二因子校验（动态码或备份码） ----------

// verifySecondFactor 返回 (方式, 是否通过, 是否因重放被拒)。
func (s *webServer) verifySecondFactor(code string) (method string, ok, reused bool) {
	c := s.creds()
	code = strings.TrimSpace(code)
	if len(code) == 6 && isAllDigits(code) && len(c.totpSecret) > 0 {
		counter, valid := verifyTOTP(c.totpSecret, code)
		if !valid {
			return "", false, false
		}
		s.totpMu.Lock()
		dup := counter <= s.totpLast
		if !dup {
			s.totpLast = counter
		}
		s.totpMu.Unlock()
		if dup {
			return "totp", false, true
		}
		return "totp", true, false
	}
	// 备份码
	if len(c.backupHashes) == 0 {
		return "", false, false
	}
	h := hashBackupCode(code)
	for i, bh := range c.backupHashes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(bh)) != 1 {
			continue
		}
		var remaining []string
		s.updateCred(func(st *credState) {
			if i < len(st.backupHashes) {
				remaining = append(append([]string{}, st.backupHashes[:i]...), st.backupHashes[i+1:]...)
				st.backupHashes = remaining
			}
		})
		if remaining != nil {
			_ = saveBackupHashes(c.backupFile, remaining)
		}
		return "backup", true, false
	}
	return "", false, false
}

// ---------- 2FA 管理接口 ----------

type twofaStatusResp struct {
	Policy      bool `json:"policy"`
	Configured  bool `json:"configured"`
	BackupCodes int  `json:"backupCodes"`
}

func (s *webServer) handle2FA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	c := s.creds()
	jsonWrite(w, http.StatusOK, twofaStatusResp{
		Policy:      c.twofaPolicy,
		Configured:  c.totpEnabled,
		BackupCodes: len(c.backupHashes),
	})
}

func (s *webServer) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	c := s.creds()
	if !c.twofaPolicy {
		jsonErr(w, http.StatusBadRequest, "2fa_disabled", "服务端未启用 2FA 策略（需要 --web-2fa totp）")
		return
	}
	if c.totpEnabled {
		jsonErr(w, http.StatusConflict, "already_configured", "2FA 已配置；如需重新设置请先关闭")
		return
	}
	secretStr, err := newTOTPSecret()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal", "生成 secret 失败")
		return
	}
	account := c.user
	if account == "" {
		account = "admin"
	}
	jsonWrite(w, http.StatusOK, map[string]string{
		"secret": secretStr,
		"uri":    otpauthURI("rbackup-ui", account, secretStr),
	})
}

func (s *webServer) handle2FASetupConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	c := s.creds()
	if !c.twofaPolicy || c.totpEnabled {
		jsonErr(w, http.StatusBadRequest, "invalid_state", "当前状态不允许设置 2FA")
		return
	}
	ip := s.clientIP(r)
	if !s.limiter.allow(ip) {
		jsonErr(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
		return
	}
	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
		return
	}
	secret, err := decodeTOTPSecret(req.Secret)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid_secret", "secret 格式错误")
		return
	}
	counter, ok := verifyTOTP(secret, req.Code)
	if !ok {
		s.limiter.fail(ip)
		jsonErr(w, http.StatusUnauthorized, "invalid_code", "动态码不正确，请确认手机时间准确后重试")
		return
	}
	secretStr := strings.TrimRight(strings.ToUpper(strings.TrimSpace(req.Secret)), "=")
	if err := writeFileAtomic(c.secretFile, []byte(secretStr+"\n"), 0o600); err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal", "保存 secret 失败: "+err.Error())
		return
	}
	plain, hashes := newBackupCodes(backupCodeCount)
	if err := saveBackupHashes(c.backupFile, hashes); err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal", "保存备份码失败: "+err.Error())
		return
	}
	s.updateCred(func(st *credState) {
		st.totpSecret = secret
		st.totpEnabled = true
		st.backupHashes = hashes
	})
	s.totpMu.Lock()
	s.totpLast = counter
	s.totpMu.Unlock()
	s.audit.log("2fa.setup", ip, r.UserAgent(), nil)
	jsonWrite(w, http.StatusOK, map[string]any{
		"backupCodes": formatBackupCodes(plain),
	})
}

func (s *webServer) handle2FABackupCodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	c := s.creds()
	if !c.totpEnabled {
		jsonErr(w, http.StatusBadRequest, "2fa_disabled", "尚未启用 2FA")
		return
	}
	ip := s.clientIP(r)
	if !s.limiter.allow(ip) {
		jsonErr(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
		return
	}
	method, ok, _ := s.verifySecondFactor(req.Code)
	if !ok {
		s.limiter.fail(ip)
		s.audit.log("2fa.backup_regen_fail", ip, r.UserAgent(), nil)
		jsonErr(w, http.StatusUnauthorized, "invalid_code", "动态码或备份码不正确")
		return
	}
	plain, hashes := newBackupCodes(backupCodeCount)
	if err := saveBackupHashes(c.backupFile, hashes); err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal", "保存备份码失败: "+err.Error())
		return
	}
	s.updateCred(func(st *credState) { st.backupHashes = hashes })
	s.audit.log("2fa.backup_regen", ip, r.UserAgent(), map[string]any{"method": method})
	jsonWrite(w, http.StatusOK, map[string]any{"backupCodes": formatBackupCodes(plain)})
}

func (s *webServer) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	c := s.creds()
	if !c.totpEnabled {
		jsonErr(w, http.StatusBadRequest, "2fa_disabled", "尚未启用 2FA")
		return
	}
	ip := s.clientIP(r)
	if !s.limiter.allow(ip) {
		jsonErr(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
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
		s.limiter.fail(ip)
		code := "invalid_code"
		if reused {
			code = "code_reused"
		}
		s.audit.log("2fa.disable_fail", ip, r.UserAgent(), nil)
		jsonErr(w, http.StatusUnauthorized, code, "动态码或备份码不正确（用过的码不能复用）")
		return
	}
	_ = os.Remove(c.secretFile)
	_ = os.Remove(c.backupFile)
	s.updateCred(func(st *credState) {
		st.totpSecret = nil
		st.totpEnabled = false
		st.backupHashes = nil
	})
	s.audit.log("2fa.disable", ip, r.UserAgent(), map[string]any{"method": method})
	jsonWrite(w, http.StatusOK, map[string]any{"ok": true})
}

func formatBackupCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, formatBackupCode(c))
	}
	return out
}

// ---------- 手机扫码登录 ----------

type phoneLink struct {
	expires time.Time
	used    bool
}

func hashPhoneToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// prunePhoneLinksLocked 清理过期链接（调用方需持有 phoneMu）。
func (s *webServer) prunePhoneLinksLocked(now time.Time) {
	for k, v := range s.phoneLinks {
		if v.used || now.After(v.expires) {
			delete(s.phoneLinks, k)
		}
	}
}

// buildPhoneHost 返回手机可访问的 host 部分（可能带端口）。
// loopback/通配监听时换成局域网 IPv4（手机扫码不能用 127.0.0.1），端口缺省时
// 按 scheme 补 80/443；域名、私网 IP、反代传入的 Host 原样保留（不补默认端口，
// 避免把 https://域名 生成成 https://域名:443 或错误的 :80）。
func buildPhoneHost(host, scheme string) string {
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		hostname, port = host, ""
	}
	if hostname == "" || hostname == "0.0.0.0" || hostname == "::" || isLoopbackHost(hostname) {
		if ip := lanIPv4(); ip != "" {
			hostname = ip
		}
		if port == "" {
			if scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
	}
	if strings.Contains(hostname, ":") && !strings.HasPrefix(hostname, "[") {
		hostname = "[" + hostname + "]"
	}
	if port == "" {
		return hostname
	}
	return hostname + ":" + port
}

func lanIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			if ip4[0] == 192 && ip4[1] == 168 {
				return ip4.String()
			}
			if fallback == "" {
				fallback = ip4.String()
			}
		}
	}
	return fallback
}

func (s *webServer) handlePhoneLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	tok := randToken(24)
	now := time.Now()
	s.phoneMu.Lock()
	s.prunePhoneLinksLocked(now)
	if len(s.phoneLinks) >= phoneLinkMax {
		s.phoneMu.Unlock()
		jsonErr(w, http.StatusTooManyRequests, "too_many_links", "一次性链接过多，请稍后再试")
		return
	}
	s.phoneLinks[hashPhoneToken(tok)] = &phoneLink{expires: now.Add(phoneLinkTTL)}
	s.phoneMu.Unlock()

	// 生成手机可打开的链接：
	// - 配置了 --web-public-url：直接用它（多入口时可固定到公网域名）；
	// - 否则按当前入口推导 scheme/host，loopback 时自动换成局域网 IP。
	base := s.publicBase(r)
	if s.opts.PublicURL == "" {
		base = s.requestScheme(r) + "://" + buildPhoneHost(s.effectiveHost(r), s.requestScheme(r))
	}
	url := base + "/#t=" + tok
	s.audit.log("phone_link.create", s.clientIP(r), r.UserAgent(), nil)
	jsonWrite(w, http.StatusOK, map[string]any{
		"url":       url,
		"expiresIn": int(phoneLinkTTL.Seconds()),
	})
}

func (s *webServer) handlePhoneSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	if !s.sameOrigin(r) {
		jsonErr(w, http.StatusForbidden, "csrf", "请求来源不被允许")
		return
	}
	ip := s.clientIP(r)
	if !s.limiter.allow(ip) {
		jsonErr(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
		return
	}
	key := hashPhoneToken(strings.TrimSpace(req.Token))
	now := time.Now()
	s.phoneMu.Lock()
	link, ok := s.phoneLinks[key]
	if ok && (link.used || now.After(link.expires)) {
		ok = false
	}
	if ok {
		link.used = true
	}
	delete(s.phoneLinks, key)
	s.phoneMu.Unlock()
	if !ok {
		s.limiter.fail(ip)
		s.audit.log("phone_link.fail", ip, r.UserAgent(), nil)
		jsonErr(w, http.StatusUnauthorized, "invalid_link", "链接无效、已过期或已被使用")
		return
	}
	s.audit.log("phone_link.used", ip, r.UserAgent(), nil)
	jsonWrite(w, http.StatusOK, s.issueSession(w, r))
}

// ---------- 服务端二维码 PNG ----------

func encodeQRPNG(text string, scale, quiet int) ([]byte, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	if scale <= 0 {
		scale = 6
	}
	if quiet < 0 {
		quiet = 0
	}
	size := code.Size + quiet*2
	img := image.NewGray(image.Rect(0, 0, size*scale, size*scale))
	white := color.Gray{Y: 255}
	black := color.Gray{Y: 0}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			on := false
			cx, cy := x-quiet, y-quiet
			if cx >= 0 && cy >= 0 && cx < code.Size && cy < code.Size {
				on = code.Black(cx, cy)
			}
			c := white
			if on {
				c = black
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *webServer) handleQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method", "不支持的方法")
		return
	}
	data := r.URL.Query().Get("data")
	if data == "" || len(data) > qrMaxDataLen {
		jsonErr(w, http.StatusBadRequest, "bad_request", "data 参数缺失或过长")
		return
	}
	pngData, err := encodeQRPNG(data, 6, 4)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal", "二维码生成失败")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pngData)
}

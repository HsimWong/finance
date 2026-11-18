package main

import (
	"crypto/subtle"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const accessCookie = "finance_access"

const maxLoginFails = 10
const loginWindow = 10 * time.Minute

// authGate 为一层可选的口令保护。ACCESS_TOKEN 为空时完全放行（仅限可信内网）。
// 公网暴露（Cloudflare 隧道）时必须设置，否则任何人拿到地址就能看到全部账本与截图。
type authGate struct {
	token string
	mu    sync.Mutex
	tries map[string]loginTry
}

type loginTry struct {
	n     int
	until time.Time
}

func newAuthGate(token string) *authGate {
	return &authGate{token: token, tries: map[string]loginTry{}}
}

func (g *authGate) enabled() bool { return g.token != "" }

func (g *authGate) clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip // Cloudflare 隧道下所有请求都来自 127.0.0.1，用边缘传来的真实 IP 限速
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (g *authGate) blocked(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.tries[ip]
	if !ok {
		return false
	}
	if time.Now().After(a.until) {
		delete(g.tries, ip)
		return false
	}
	return a.n >= maxLoginFails
}

func (g *authGate) recordFail(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a := g.tries[ip]
	if a.until.IsZero() || time.Now().After(a.until) {
		a = loginTry{until: time.Now().Add(loginWindow)}
	}
	a.n++
	g.tries[ip] = a
}

func (g *authGate) valid(v string) bool {
	return v != "" && subtle.ConstantTimeCompare([]byte(v), []byte(g.token)) == 1
}

func (g *authGate) authorized(r *http.Request) bool {
	if c, err := r.Cookie(accessCookie); err == nil && g.valid(c.Value) {
		return true
	}
	if g.valid(r.URL.Query().Get("token")) {
		return true
	}
	return g.valid(r.Header.Get("X-Access-Token"))
}

func (g *authGate) wrap(next http.Handler) http.Handler {
	if !g.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__auth" {
			g.handleLogin(w, r)
			return
		}
		if g.authorized(r) {
			// 从 URL 带口令进来时写入 Cookie，之后正常访问无需再带
			if t := r.URL.Query().Get("token"); t != "" && g.valid(t) {
				setAccessCookie(w, t)
			}
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "需要访问口令：请先用浏览器打开首页输入口令，或附带 X-Access-Token 请求头",
			})
			return
		}
		g.renderLogin(w, http.StatusUnauthorized, "")
	})
}

func setAccessCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   30 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (g *authGate) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// 相对跳转：本系统可能被挂在 /finance/ 这类子路径下（Nginx 反代）
		redirectRel(w, "./", http.StatusFound)
		return
	}
	ip := g.clientIP(r)
	if g.blocked(ip) {
		g.renderLogin(w, http.StatusTooManyRequests, "尝试次数过多，请 10 分钟后再试")
		return
	}
	_ = r.ParseForm()
	if g.valid(r.PostFormValue("token")) {
		setAccessCookie(w, r.PostFormValue("token"))
		redirectRel(w, "./", http.StatusFound)
		return
	}
	g.recordFail(ip)
	g.renderLogin(w, http.StatusUnauthorized, "口令不正确")
}

func (g *authGate) renderLogin(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = loginTmpl.Execute(w, map[string]string{"Error": msg})
}

var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>需要访问口令</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
         font: 15px/1.6 -apple-system, "Segoe UI", "Microsoft YaHei", sans-serif;
         background:#f4f5f7; color:#1f2430; padding:24px; }
  .card { width:100%; max-width:360px; background:#fff; border:1px solid #e4e7ec;
          border-radius:14px; padding:28px 24px; box-shadow:0 6px 24px rgba(16,24,40,.06); }
  h1 { margin:0 0 6px; font-size:19px; }
  .hint { margin:0 0 20px; color:#6b7280; font-size:13px; }
  .err { margin:0 0 14px; padding:9px 12px; border-radius:8px; background:#fdecec;
         color:#b42318; font-size:13px; }
  input { width:100%; padding:11px 13px; font-size:15px; border:1px solid #d0d5dd;
          border-radius:9px; outline:none; }
  input:focus { border-color:#7c5cff; box-shadow:0 0 0 3px rgba(124,92,255,.15); }
  button { width:100%; margin-top:14px; padding:11px; font-size:15px; color:#fff;
           background:#5b4bdb; border:0; border-radius:9px; cursor:pointer; }
  button:hover { background:#4a3cc4; }
  .tip { margin:18px 0 0; color:#98a2b3; font-size:12px; text-align:center; }
  @media (prefers-color-scheme: dark) {
    body { background:#14161a; color:#e7e9ee; }
    .card { background:#1c1f26; border-color:#2b303a; box-shadow:none; }
    .hint, .tip { color:#8b93a3; }
    input { background:#14161a; border-color:#333a46; color:#e7e9ee; }
    .err { background:#3a1d1d; color:#ffb4b4; }
  }
</style>
</head>
<body>
  <form class="card" method="post" action="__auth">
    <h1>家庭财务管理系统</h1>
    <p class="hint">此系统仅限本人使用，请输入访问口令。</p>
    {{if .Error}}<p class="err">{{.Error}}</p>{{end}}
    <input type="password" name="token" placeholder="访问口令" autocomplete="current-password" autofocus required>
    <button type="submit">进入</button>
    <p class="tip">口令配置在项目根目录 .env 的 ACCESS_TOKEN</p>
  </form>
</body>
</html>`))

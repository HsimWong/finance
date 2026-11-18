package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authTestHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("INDEX"))
	})
	return mux
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAuthGateBlocksAndAllows(t *testing.T) {
	h := newAuthGate("secret-token").wrap(authTestHandler())

	// ① API 未带口令 → 401 JSON
	rr := do(h, httptest.NewRequest("GET", "/api/meta", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("API 未鉴权应为 401，得到 %d", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["ok"] != false {
		t.Fatalf("401 响应体应含 ok=false，得到 %s", rr.Body.String())
	}

	// ② 页面未带口令 → 401 + 登录页
	rr = do(h, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "访问口令") {
		t.Fatalf("页面未鉴权应返回登录页，得到 %d", rr.Code)
	}

	// ③ URL 带口令 → 200 且下发 Cookie
	rr = do(h, httptest.NewRequest("GET", "/?token=secret-token", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "INDEX") {
		t.Fatalf("URL 带口令应放行，得到 %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != accessCookie {
		t.Fatalf("应下发访问 Cookie，得到 %v", cookies)
	}

	// ④ Cookie 访问 API → 200
	req := httptest.NewRequest("GET", "/api/meta", nil)
	req.AddCookie(cookies[0])
	if rr = do(h, req); rr.Code != http.StatusOK {
		t.Fatalf("Cookie 应放行，得到 %d", rr.Code)
	}

	// ⑤ 请求头带口令 → 200
	req = httptest.NewRequest("GET", "/api/meta", nil)
	req.Header.Set("X-Access-Token", "secret-token")
	if rr = do(h, req); rr.Code != http.StatusOK {
		t.Fatalf("X-Access-Token 应放行，得到 %d", rr.Code)
	}

	// ⑥ 错误口令 → 401
	if rr = do(h, httptest.NewRequest("GET", "/?token=wrong", nil)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("错误口令应拒绝，得到 %d", rr.Code)
	}

	// ⑦ 登录表单：错误口令 401，正确口令 302 + Cookie
	form := "token=wrong"
	req = httptest.NewRequest("POST", "/__auth", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rr = do(h, req); rr.Code != http.StatusUnauthorized {
		t.Fatalf("错误口令登录应 401，得到 %d", rr.Code)
	}
	req = httptest.NewRequest("POST", "/__auth", strings.NewReader("token=secret-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = do(h, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("正确口令登录应 302，得到 %d", rr.Code)
	}
	if len(rr.Result().Cookies()) == 0 {
		t.Fatal("登录成功应下发 Cookie")
	}
}

func TestAuthGateDisabledWhenNoToken(t *testing.T) {
	h := newAuthGate("").wrap(authTestHandler())
	rr := do(h, httptest.NewRequest("GET", "/api/meta", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("未设置口令时应放行，得到 %d", rr.Code)
	}
	bs, _ := io.ReadAll(rr.Result().Body)
	if !strings.Contains(string(bs), `"ok":true`) {
		t.Fatalf("响应异常: %s", bs)
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// PWA 安装时浏览器发起的 manifest 请求不带页面 Cookie，必须匿名放行。
// 只放行清单/SW/图标；API 和页面仍需口令。
func TestPublicStaticBypassesAuth(t *testing.T) {
	g := newAuthGate("test-token")
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := g.wrap(inner)

	for _, p := range []string{"/manifest.json", "/sw.js", "/icons/icon-192.png"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNoContent {
			t.Errorf("PWA 静态资源 %s 应匿名放行，实际 %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/api/meta", "/mobile.html", "/index.html", "/api/entries"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s 仍应要求口令，实际 %d", p, rec.Code)
		}
	}
}

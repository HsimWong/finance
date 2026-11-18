package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 隧道日志里出现多个域名时必须取最后一个：cloudflared 重连会追加新域名，
// 旧域名已被回收，报上去就是死链。
func TestTunnelURLFromLogTakesLast(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tunnel.log")
	content := strings.Join([]string{
		`INF +--------------------------------------------------------------------------------------------+`,
		`INF |  https://old-name-dead.trycloudflare.com                                                   |`,
		`INF +--------------------------------------------------------------------------------------------+`,
		`INF Registered tunnel connection connIndex=0 location=sin10`,
		`ERR failed to serve tunnel connection, retrying`,
		`INF |  https://new-name-alive.trycloudflare.com                                                 |`,
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := tunnelURLFromLog(logPath); got != "https://new-name-alive.trycloudflare.com" {
		t.Fatalf("应取最后一个域名，得到 %q", got)
	}
}

func TestTunnelURLFromLogEmpty(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"文件不存在":   filepath.Join(dir, "nope.log"),
		"日志里没有域名": writeTemp(t, dir, "tunnel.log", "INF Starting tunnel\nINF connection registered\n"),
	}
	for name, path := range cases {
		if got := tunnelURLFromLog(path); got != "" {
			t.Fatalf("%s：应返回空串，得到 %q", name, got)
		}
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 上报要打到 /api/register（查询串跟在路径后），并带上 token 与三个字段。
func TestReporterRegister(t *testing.T) {
	var gotPath, gotQuery, gotToken string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotToken = r.URL.Path, r.URL.RawQuery, r.Header.Get("X-GW-Token")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	g := &GatewayReporter{
		base:   srv.URL,
		query:  "?sharecode=ABC123",
		token:  "secret-token",
		prefix: "finance",
		name:   "家庭财务系统",
		client: srv.Client(),
	}
	if err := g.register("https://abc-def.trycloudflare.com"); err != nil {
		t.Fatalf("上报应成功: %v", err)
	}

	if gotPath != "/api/register" {
		t.Fatalf("路径不对: %q", gotPath)
	}
	if gotQuery != "sharecode=ABC123" {
		t.Fatalf("查询串应保留 sharecode，得到 %q", gotQuery)
	}
	if gotToken != "secret-token" {
		t.Fatalf("缺少网关口令，得到 %q", gotToken)
	}
	if gotBody["prefix"] != "finance" || gotBody["target"] != "https://abc-def.trycloudflare.com" {
		t.Fatalf("请求体不对: %v", gotBody)
	}
}

// 网关拒绝（口令错）时要报错，不能当成成功，否则日志会把「入口没更新」说成正常。
func TestReporterRegisterRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"ok":false,"error":"token 不正确"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	g := &GatewayReporter{base: srv.URL, client: srv.Client()}
	err := g.register("https://abc-def.trycloudflare.com")
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("错误信息应含状态码，得到 %v", err)
	}
}

// 没有隧道域名时（--local 模式或隧道未起）应直接跳过，不该发出任何请求。
func TestReporterCheckSkipsWithoutTunnel(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()

	g := &GatewayReporter{
		base:      srv.URL,
		tunnelLog: filepath.Join(t.TempDir(), "tunnel.log"), // 不存在
		client:    srv.Client(),
		interval:  time.Second,
	}
	g.check(true)
	if hit {
		t.Fatal("没有隧道域名时不应上报")
	}
}

// 有隧道域名时要真的把域名报上去。
func TestReporterCheckReports(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	logPath := writeTemp(t, dir, "tunnel.log", "INF | https://live-one.trycloudflare.com |\n")
	g := &GatewayReporter{
		base: srv.URL, prefix: "finance", name: "家庭财务系统",
		tunnelLog: logPath, client: srv.Client(), interval: time.Second,
	}
	g.check(true)
	if gotBody["target"] != "https://live-one.trycloudflare.com" {
		t.Fatalf("上报的域名不对: %v", gotBody)
	}
	if g.lastSeen != "https://live-one.trycloudflare.com" {
		t.Fatalf("应记住已上报的域名，得到 %q", g.lastSeen)
	}
}

// 域名没变时，常规扫描（force=false）不该再吵网关；这是 5 秒一扫不刷屏的前提。
func TestReporterCheckSkipsUnchanged(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	logPath := writeTemp(t, t.TempDir(), "tunnel.log", "INF | https://same-one.trycloudflare.com |\n")
	g := &GatewayReporter{base: srv.URL, tunnelLog: logPath, client: srv.Client(), interval: time.Second}

	g.check(false) // 首次扫描：没见过这个域名，要报
	g.check(false) // 域名没变：跳过
	g.check(false)
	if hits != 1 {
		t.Fatalf("域名未变时只应上报 1 次，实际 %d 次", hits)
	}
	g.check(true) // 心跳强制重报
	if hits != 2 {
		t.Fatalf("心跳应强制上报，实际 %d 次", hits)
	}
}

// 隧道重连换了域名时，5 秒扫描要能马上改报，而不是等到下一个心跳。
func TestReporterCheckReportsChangedDomain(t *testing.T) {
	var targets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		targets = append(targets, body["target"])
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	logPath := writeTemp(t, dir, "tunnel.log", "INF | https://first-one.trycloudflare.com |\n")
	g := &GatewayReporter{base: srv.URL, tunnelLog: logPath, client: srv.Client(), interval: time.Hour}

	g.check(false)
	// cloudflared 重连，追加新域名
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("INF | https://second-one.trycloudflare.com |\n")
	f.Close()

	g.check(false)
	if len(targets) != 2 || !strings.Contains(targets[1], "second-one") {
		t.Fatalf("域名变化应立即改报，得到 %v", targets)
	}
}

// 上报失败后要退避，别每 5 秒撞一次门。
func TestReporterCheckBacksOffAfterFailure(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	logPath := writeTemp(t, t.TempDir(), "tunnel.log", "INF | https://x-one.trycloudflare.com |\n")
	g := &GatewayReporter{base: srv.URL, tunnelLog: logPath, client: srv.Client(), interval: time.Second}

	g.check(false)
	g.check(false) // 退避期内应直接返回
	if hits != 1 {
		t.Fatalf("退避期内不应重试，实际请求 %d 次", hits)
	}
}

// 没配 GATEWAY_URL 时不上报器，避免默认行为里凭空多出一个网络请求。
func TestNewReporterDisabledWithoutURL(t *testing.T) {
	if g := newGatewayReporter(Config{}); g != nil {
		t.Fatal("未配 GATEWAY_URL 时应返回 nil")
	}
	if g := newGatewayReporter(Config{GatewayURL: "https://gw.example.com/?sharecode=X"}); g == nil {
		t.Fatal("配了 GATEWAY_URL 应启用")
	} else if g.base != "https://gw.example.com" || g.query != "?sharecode=X" {
		t.Fatalf("地址拆分不对: base=%q query=%q", g.base, g.query)
	}
}

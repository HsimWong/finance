package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// 隧道临时域名形如 https://three-random-words.trycloudflare.com
var tunnelURLRe = regexp.MustCompile(`https://[a-z0-9][a-z0-9-]*\.trycloudflare\.com`)

// tunnelURLFromLog 取隧道日志里最后一次出现的域名。
// cloudflared 断线重连会追加新域名，旧域名随即被回收，所以必须用最后一个而不是第一个。
func tunnelURLFromLog(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	all := tunnelURLRe.FindAll(b, -1)
	if len(all) == 0 {
		return ""
	}
	return string(all[len(all)-1])
}

// 域名变化检测的扫描间隔。读一个日志文件几乎零成本，扫得勤是为了压缩
// 「隧道换了域名、网关还指着旧域名」那段 530 空窗。心跳仍按 GATEWAY_INTERVAL 走。
const tunnelScanInterval = 5 * time.Second

// 上报失败后的退避时间：网关挂了不要每 5 秒撞一次门。
const gatewayRetryAfter = 30 * time.Second

// GatewayReporter 定期把当前公网地址上报给固定入口网关。
//
// 为什么放在服务进程里而不是启动脚本里：脚本的前台循环一旦结束（终端关掉、SSH 断开、
// 脚本被杀），固定入口就再没人更新；而且网关沙箱会被回收重启、注册表随之清空，
// 只有持续心跳才能让入口自己长回来。只要账本在跑，上报就一直在。
type GatewayReporter struct {
	base, query         string // 网关地址已拆成「不含查询串」+「?sharecode=...」
	token, prefix, name string
	tunnelLog           string
	interval            time.Duration

	client *http.Client // 上报目标是公网，走环境代理（若有）

	lastSeen   string    // 日志里最近一次看到、且已成功上报的域名
	lastErr    string    // 上次失败原因，用于「同样错误只打一次」
	lastLog    time.Time // 上次写心跳成功日志的时间
	retryAfter time.Time // 失败退避截止时间
}

func newGatewayReporter(cfg Config) *GatewayReporter {
	if cfg.GatewayURL == "" {
		return nil // 没配网关，功能整体关闭
	}
	base, query := cfg.GatewayURL, ""
	// WorkBuddy 的分享链接带 ?sharecode=...，拼接口地址时查询串要跟在路径后面，
	// 不能插在路径中间，所以先拆开。
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base, query = base[:i], base[i:]
	}
	return &GatewayReporter{
		base:      strings.TrimRight(base, "/"),
		query:     query,
		token:     cfg.GatewayToken,
		prefix:    cfg.GatewayPrefix,
		name:      cfg.GatewayName,
		tunnelLog: cfg.TunnelLog,
		interval:  cfg.GatewayInterval,
		client:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *GatewayReporter) start() {
	log.Printf("固定入口: %s/%s/（域名变化 %s 内改报，心跳每 %s）", g.base, g.prefix, tunnelScanInterval, g.interval)
	go g.loop()
}

func (g *GatewayReporter) loop() {
	scan := time.NewTicker(tunnelScanInterval)
	defer scan.Stop()
	beat := time.NewTicker(g.interval)
	defer beat.Stop()

	g.check(true) // 启动立刻报一次，别让固定入口空等
	for {
		select {
		case <-scan.C:
			g.check(false) // 域名变了才报
		case <-beat.C:
			g.check(true) // 无条件重报：网关沙箱重启后靠它把注册表长回来
		}
	}
}

// check 是上报入口。force=false 时只在隧道域名变化后才真正发请求。
func (g *GatewayReporter) check(force bool) {
	if time.Now().Before(g.retryAfter) {
		return // 刚失败过，等退避结束
	}
	target := tunnelURLFromLog(g.tunnelLog)
	if target == "" {
		// 隧道还没起来（或跑的是 --local）。这不是错误，静默等待。
		return
	}
	if target == g.lastSeen && !force {
		return
	}
	if err := g.register(target); err != nil {
		g.retryAfter = time.Now().Add(gatewayRetryAfter)
		// 同样的错误只打一次，否则网关一晚上不可用就会刷屏几千行。
		if msg := err.Error(); msg != g.lastErr {
			g.lastErr = msg
			log.Printf("固定入口上报失败: %v（固定入口可能暂时打不开，本机与局域网不受影响）", err)
		}
		return
	}
	if g.lastErr != "" {
		g.lastErr = ""
		log.Printf("固定入口上报已恢复")
	}

	changed := target != g.lastSeen
	g.lastSeen = target
	switch {
	case changed:
		// 域名变了：这是必须打出来的一条，手机上的固定入口就是从死链切到新链的时刻。
		g.lastLog = time.Now()
		log.Printf("固定入口已指向 %s", target)
	case time.Since(g.lastLog) >= 30*time.Minute:
		// 平时心跳成功太频繁，半小时留一条痕迹即可，证明「它还活着」。
		g.lastLog = time.Now()
		log.Printf("固定入口心跳正常（%s）", target)
	}
}

// register 把域名推给网关。幂等：同一个 prefix 反复上报只是更新 target。
func (g *GatewayReporter) register(target string) error {
	body, err := json.Marshal(map[string]string{
		"name":   g.name,
		"prefix": g.prefix,
		"target": target,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, g.base+"/api/register"+g.query, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.token != "" {
		req.Header.Set("X-GW-Token", g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("网关返回 HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

package main

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config 只承载启动期配置，不引入任何第三方配置库（保持无框架约定）。
// 优先级：命令行 -addr > 环境变量 > .env > 默认值。
type Config struct {
	Addr   string // 最终监听地址，如 127.0.0.1:8081 / 0.0.0.0:8081
	Host   string
	Port   string
	Source string // 便于排查「为什么是这个地址」

	AccessToken string // 空 = 不启用口令保护（仅限可信内网）
	TokenSource string

	// 固定入口网关上报名（全部可选）。GatewayURL 为空即视为没配，整个上报功能关闭。
	GatewayURL      string
	GatewayToken    string
	GatewayPrefix   string
	GatewayName     string
	GatewayInterval time.Duration
	TunnelLog       string // cloudflared 日志，用来取当前临时域名
}

// loadEnvFile 解析 .env：支持 KEY=VALUE、export 前缀、引号包裹、整行 # 注释。
// 故意不解析行尾 # 注释，避免路径或值里含 # 被截断。
func loadEnvFile(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out // 没有 .env 属于正常情况，走默认值
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// pick 先读环境变量再读 .env，空值视为未设置。
func pick(key string, file map[string]string) (string, string) {
	if v, ok := os.LookupEnv(key); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v, "环境变量 " + key
		}
	}
	if v, ok := file[key]; ok {
		if v = strings.TrimSpace(v); v != "" {
			return v, ".env 的 " + key
		}
	}
	return "", ""
}

func loadConfig(root, envPath, flagAddr string) Config {
	path := envPath
	if path == "" {
		path = filepath.Join(root, ".env")
	}
	file := loadEnvFile(path)
	cfg := Config{}

	if flagAddr != "" {
		cfg.Addr, cfg.Source = flagAddr, "命令行 -addr"
	} else if v, src := pick("HTTP_ADDR", file); v != "" {
		cfg.Addr, cfg.Source = v, src
	} else {
		host, hsrc := pick("BIND_HOST", file)
		port, psrc := pick("BIND_PORT", file)
		if host == "" {
			host = "0.0.0.0"
		}
		if port == "" {
			port = "8081"
		}
		switch {
		case hsrc != "" && psrc != "":
			cfg.Source = hsrc + " + " + psrc
		case hsrc != "":
			cfg.Source = hsrc + "（端口用默认 8081）"
		case psrc != "":
			cfg.Source = psrc + "（host 用默认 0.0.0.0）"
		default:
			cfg.Source = "默认值（未找到 .env，可用 -env 指定路径）"
		}
		cfg.Addr = net.JoinHostPort(host, port)
	}

	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		log.Fatalf("监听地址格式错误 %q：%v（示例：127.0.0.1:8081）", cfg.Addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		log.Fatalf("端口非法 %q：应为 1-65535", port)
	}
	if host != "" && net.ParseIP(host) == nil {
		log.Fatalf("BIND_HOST 非法 %q：应为 IP，如 127.0.0.1 / 0.0.0.0 / 192.168.1.10", host)
	}
	cfg.Host, cfg.Port = host, port
	cfg.AccessToken, cfg.TokenSource = pick("ACCESS_TOKEN", file)

	// 固定入口网关：全部可选，不配就像以前一样只在临时域名/局域网可用。
	cfg.GatewayURL, _ = pick("GATEWAY_URL", file)
	cfg.GatewayToken, _ = pick("GATEWAY_TOKEN", file)
	if cfg.GatewayPrefix, _ = pick("GATEWAY_PREFIX", file); cfg.GatewayPrefix == "" {
		cfg.GatewayPrefix = "finance"
	}
	if cfg.GatewayName, _ = pick("GATEWAY_NAME", file); cfg.GatewayName == "" {
		cfg.GatewayName = "家庭财务系统"
	}
	cfg.GatewayInterval = 60 * time.Second
	if v, _ := pick("GATEWAY_INTERVAL", file); v != "" {
		// 下限 10s：上报是幂等 upsert，但没必要把网关当心跳靶子打。
		if d, err := time.ParseDuration(v); err == nil && d >= 10*time.Second {
			cfg.GatewayInterval = d
		} else {
			log.Printf("警告：GATEWAY_INTERVAL=%q 无效（需 ≥10s，如 60s），改用 60s", v)
		}
	}
	if cfg.TunnelLog, _ = pick("GATEWAY_TUNNEL_LOG", file); cfg.TunnelLog == "" {
		cfg.TunnelLog = filepath.Join(root, "logs", "tunnel.log")
	}
	return cfg
}

// listenURLs 生成便于直接点开的访问地址；绑定 0.0.0.0 时额外列出局域网地址，
// 方便手机连同一 WiFi 上传账单截图。
func listenURLs(cfg Config) []string {
	if cfg.Host != "" && cfg.Host != "0.0.0.0" && cfg.Host != "::" {
		return []string{"http://" + net.JoinHostPort(cfg.Host, cfg.Port)}
	}
	out := []string{"http://localhost:" + cfg.Port}
	for _, ip := range lanIPv4() {
		out = append(out, "http://"+net.JoinHostPort(ip, cfg.Port))
	}
	return out
}

func lanIPv4() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
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
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || !ip.IsPrivate() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}

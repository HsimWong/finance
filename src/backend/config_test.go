package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 验证 .env 解析与优先级：命令行 > 环境变量 > .env > 默认值。
func TestConfigPriority(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte(
		"# 注释行\nBIND_HOST=127.0.0.1\nBIND_PORT=9099\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("BIND_HOST")
	os.Unsetenv("BIND_PORT")

	cfg := loadConfig(dir, "", "")
	if cfg.Addr != "127.0.0.1:9099" {
		t.Fatalf(".env 未生效，得到 %q", cfg.Addr)
	}
	if !strings.Contains(cfg.Source, ".env") {
		t.Fatalf("来源标识不对: %s", cfg.Source)
	}

	// 环境变量应覆盖 .env
	os.Setenv("BIND_PORT", "9199")
	defer os.Unsetenv("BIND_PORT")
	if cfg = loadConfig(dir, "", ""); cfg.Addr != "127.0.0.1:9199" {
		t.Fatalf("环境变量未覆盖 .env，得到 %q", cfg.Addr)
	}

	// 命令行应覆盖环境变量
	if cfg = loadConfig(dir, "", "0.0.0.0:9299"); cfg.Addr != "0.0.0.0:9299" {
		t.Fatalf("命令行未生效，得到 %q", cfg.Addr)
	}
	if !strings.Contains(cfg.Source, "-addr") {
		t.Fatalf("来源标识不对: %s", cfg.Source)
	}

	// HTTP_ADDR 整体覆盖 BIND_HOST/BIND_PORT
	os.Unsetenv("BIND_PORT")
	if err := os.WriteFile(envPath, []byte(
		"BIND_HOST=127.0.0.1\nBIND_PORT=9099\nHTTP_ADDR=192.168.1.10:7000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg = loadConfig(dir, "", ""); cfg.Addr != "192.168.1.10:7000" {
		t.Fatalf("HTTP_ADDR 未生效，得到 %q", cfg.Addr)
	}

	// 没有 .env 时回落默认值
	if cfg = loadConfig(t.TempDir(), "", ""); cfg.Addr != "0.0.0.0:8081" {
		t.Fatalf("默认值不对，得到 %q", cfg.Addr)
	}
}

// 固定入口网关配置：不配就关闭；配了要有默认前缀/名字、间隔默认 60s，
// 非法的 GATEWAY_INTERVAL 要回落默认而不是启动失败。
func TestConfigGateway(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// 什么都不配：上报器应当整体关闭
	if cfg := loadConfig(dir, "", ""); cfg.GatewayURL != "" {
		t.Fatalf("未配置时 GatewayURL 应为空，得到 %q", cfg.GatewayURL)
	} else if newGatewayReporter(cfg) != nil {
		t.Fatal("未配置时不应创建上报器")
	}

	// 只配 URL：前缀/名字/间隔都要有默认值
	if err := os.WriteFile(envPath, []byte("GATEWAY_URL=https://gw.example.com/?sharecode=X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("GATEWAY_URL")
	cfg := loadConfig(dir, "", "")
	if cfg.GatewayPrefix != "finance" || cfg.GatewayName != "家庭财务系统" {
		t.Fatalf("默认前缀/名字不对: %q %q", cfg.GatewayPrefix, cfg.GatewayName)
	}
	if cfg.GatewayInterval != 60*time.Second {
		t.Fatalf("默认间隔应为 60s，得到 %v", cfg.GatewayInterval)
	}
	if !strings.HasSuffix(cfg.TunnelLog, filepath.Join("logs", "tunnel.log")) {
		t.Fatalf("隧道日志默认路径不对: %q", cfg.TunnelLog)
	}

	// 合法间隔生效
	if err := os.WriteFile(envPath, []byte(
		"GATEWAY_URL=https://gw.example.com\nGATEWAY_INTERVAL=30s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg = loadConfig(dir, "", ""); cfg.GatewayInterval != 30*time.Second {
		t.Fatalf("GATEWAY_INTERVAL 未生效，得到 %v", cfg.GatewayInterval)
	}

	// 非法/过小的间隔回落 60s，不能拖垮启动
	for _, bad := range []string{"abc", "1s", "0"} {
		if err := os.WriteFile(envPath, []byte(
			"GATEWAY_URL=https://gw.example.com\nGATEWAY_INTERVAL="+bad+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if cfg = loadConfig(dir, "", ""); cfg.GatewayInterval != 60*time.Second {
			t.Fatalf("非法间隔 %q 应回落 60s，得到 %v", bad, cfg.GatewayInterval)
		}
	}
}

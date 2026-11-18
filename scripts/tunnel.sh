#!/usr/bin/env bash
# 启动 Cloudflare 快速隧道，把本机服务临时暴露到公网。
# 本机 QUIC(UDP 7844) 被网络阻断，必须用 --protocol http2，否则边缘会返回 530。
# 用法：bash scripts/tunnel.sh
set -euo pipefail

cd "$(dirname "$0")/.."

PORT=8081
if [ -f .env ]; then
  p=$(grep -E '^BIND_PORT=' .env | cut -d= -f2- | tr -d '[:space:]')
  [ -n "${p:-}" ] && PORT="$p"
fi

if ! command -v cloudflared >/dev/null 2>&1; then
  echo "未找到 cloudflared，请先安装：winget install --id Cloudflare.cloudflared" >&2
  exit 1
fi

if ! grep -qE '^ACCESS_TOKEN=.+' .env 2>/dev/null; then
  echo "警告：.env 未设置 ACCESS_TOKEN，公网暴露将没有任何访问保护。" >&2
  echo "      建议先设置口令，再启动隧道。" >&2
fi

echo "将 http://127.0.0.1:${PORT} 暴露到公网（HTTP/2）..."
echo "启动后日志里的 https://xxx.trycloudflare.com 即公网地址；每次重启都会变化。"
exec cloudflared tunnel --url "http://127.0.0.1:${PORT}" --protocol http2 --no-autoupdate

#!/usr/bin/env bash
# 常驻守护：服务与隧道的自愈外壳。
#
# 由 Windows 计划任务托管（见 scripts/daemon.sh），因此**不依附任何终端会话**：
# 关掉终端、断开远程桌面、WorkBuddy 会话结束，进程都不会被回收。
#
# 用法：
#   supervisor.sh service   守护 finance.exe，退出后 5 秒重启
#   supervisor.sh tunnel    守护 cloudflared，换域名/DNS 不通时自动重建
#
# 停止：存在 scripts/bin/stop.flag 时不再重启（由 daemon.sh stop 写入）。
set -u

ROLE="${1:-}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/scripts/bin"
LOG="$ROOT/logs"
STOP_FLAG="$BIN/stop.flag"
mkdir -p "$BIN" "$LOG"

say() { printf '[%s] [%s] %s\n' "$(date '+%F %T')" "$ROLE" "$*" >>"$LOG/daemon.log"; }

# 计划任务里的 PATH 可能不含 cloudflared 安装目录，显式兜底。
CF="${CLOUDFLARED:-cloudflared}"
if ! command -v "$CF" >/dev/null 2>&1; then
  CF="/c/Program Files (x86)/cloudflared/cloudflared.exe"
  [ -x "$CF" ] || CF="/c/Program Files (x86)/cloudflared/cloudflared"
fi

# 清掉终端里可能注入的代理变量：cloudflared 继承它们会连边缘 7844 超时。
unset_proxy() {
  env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
      -u http_proxy -u https_proxy -u all_proxy "$@"
}

run_service() {
  local pid code
  while :; do
    if [ -f "$STOP_FLAG" ]; then say "检测到 stop.flag，服务守护退出"; exit 0; fi
    say "启动 finance.exe"
    "$BIN/finance.exe" -root "$ROOT" >>"$LOG/service.log" 2>&1 &
    pid=$!
    echo "$pid" >"$BIN/service.pid"
    wait "$pid"
    code=$?
    # 端口被占用时 1 秒内就会退出，别打转
    say "finance.exe 退出(code=$code)，5 秒后重启"
    sleep 5
  done
}

run_tunnel() {
  local cf url code ok fails=0
  while :; do
    if [ -f "$STOP_FLAG" ]; then say "检测到 stop.flag，隧道守护退出"; exit 0; fi

    : >"$LOG/tunnel.log"
    unset_proxy "$CF" tunnel --url http://127.0.0.1:8081 \
      --protocol http2 --no-autoupdate >>"$LOG/tunnel.log" 2>&1 &
    cf=$!
    echo "$cf" >"$BIN/tunnel.pid"

    # 等 quick tunnel 分配域名
    url=""
    for _ in $(seq 1 60); do
      url="$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$LOG/tunnel.log" 2>/dev/null | tail -1)"
      [ -n "$url" ] && break
      sleep 1
    done
    if [ -z "$url" ]; then
      say "隧道 60 秒内没拿到域名，重建"
      kill "$cf" 2>/dev/null
      wait "$cf" 2>/dev/null
      fails=$((fails + 1))
      [ "$fails" -ge 3 ] && { say "连续 $fails 次失败，等 30 秒"; sleep 30; fails=0; } || sleep 5
      continue
    fi

    # 域名分到了但 DNS 没建起来（NXDOMAIN）时，等也没用，只能换一个
    ok=0
    for _ in $(seq 1 15); do
      code="$(curl -s -m 8 -o /dev/null -w '%{http_code}' "$url/api/meta" 2>/dev/null)"
      case "$code" in 000|"") sleep 2 ;; *) ok=1; break ;; esac
    done
    if [ "$ok" != 1 ]; then
      say "域名 $url 公网不可达（DNS 未生效），换一个"
      kill "$cf" 2>/dev/null
      wait "$cf" 2>/dev/null
      sleep 3
      continue
    fi

    say "隧道就绪 $url"
    fails=0
    # 进程活着就一直等；它退出（断网/被踢）才重建
    wait "$cf"
    code=$?
    say "cloudflared 退出(code=$code)，5 秒后重建"
    sleep 5
  done
}

case "$ROLE" in
  service) run_service ;;
  tunnel)  run_tunnel ;;
  *) echo "用法: $0 service|tunnel" >&2; exit 2 ;;
esac

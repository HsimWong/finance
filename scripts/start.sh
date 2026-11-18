#!/usr/bin/env bash
# 家庭财务系统 · 一键启动（含 Cloudflare 隧道部署流程）
#
#   bash scripts/start.sh              编译（按需）→ 启动服务 → 建隧道 → 打印公网地址与口令
#   bash scripts/start.sh --local      只启动服务，不建隧道（仅局域网访问）
#   bash scripts/start.sh --restart    先停掉已有进程再启动
#   bash scripts/start.sh --stop       停止服务与隧道
#
# 监听地址、端口、访问口令全部读根目录 .env（BIND_HOST / BIND_PORT / ACCESS_TOKEN）。
# 日志：logs/service.log、logs/tunnel.log
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/scripts/bin/finance.exe"
LOGDIR="$ROOT/logs"
PIDDIR="$ROOT/scripts/bin"
ENVFILE="$ROOT/.env"
SVC_PID_FILE="$PIDDIR/service.pid"
TUN_PID_FILE="$PIDDIR/tunnel.pid"
mkdir -p "$LOGDIR" "$PIDDIR"

MODE="start"
for a in "$@"; do
  case "$a" in
    --local|--no-tunnel) MODE="local" ;;
    --stop) MODE="stop" ;;
    --restart) MODE="restart" ;;
    -h|--help) sed -n '/^# 家庭财务系统/,/^set -uo/p' "${BASH_SOURCE[0]}" | sed -e 's/^# \{0,1\}//' -e '/^set -uo/d'; exit 0 ;;
    *) echo "未知参数: $a（用 --help 查看用法）"; exit 1 ;;
  esac
done

# ---------- 读 .env ----------
env_get() {
  [ -f "$ENVFILE" ] || return 0
  grep -E "^[[:space:]]*$1[[:space:]]*=" "$ENVFILE" 2>/dev/null \
    | tail -1 | cut -d= -f2- | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//'
}
HOST="$(env_get BIND_HOST)"; HOST="${HOST:-0.0.0.0}"
PORT="$(env_get BIND_PORT)"; PORT="${PORT:-8081}"
TOKEN="$(env_get ACCESS_TOKEN)"
LOCAL="http://127.0.0.1:$PORT"

# ---------- 计划任务托管优先 ----------
# 装了计划任务（bash scripts/daemon.sh install）之后，进程归 Windows 任务计划所有，
# 由 scripts/supervisor.sh 守护。此时直接用 taskkill 停服务，会在 5 秒内被拉回来，
# 看起来像「停不掉」，所以启动/停止一律转交给 daemon.sh。
if command -v schtasks >/dev/null 2>&1 && schtasks //query //tn FinanceService >/dev/null 2>&1; then
  case "$MODE" in
    local) echo "服务已由计划任务常驻托管，本机地址: $LOCAL"
           echo "查看状态: bash scripts/daemon.sh status"; exit 0 ;;
    *)     # start / restart / stop 都是 daemon.sh 的子命令，直接转交
           exec bash "$ROOT/scripts/daemon.sh" "$MODE" ;;
  esac
fi

# 网关（可选）：配了 GATEWAY_URL 才会把当前隧道地址上报过去
GW_URL="$(env_get GATEWAY_URL)"
GW_TOKEN="$(env_get GATEWAY_TOKEN)"
GW_PREFIX="$(env_get GATEWAY_PREFIX)"; GW_PREFIX="${GW_PREFIX:-finance}"
GW_NAME="$(env_get GATEWAY_NAME)"; GW_NAME="${GW_NAME:-家庭财务系统}"

# ---------- 跨平台进程工具 ----------
# Git Bash 的 $! 是 MSYS 内部 pid，跟 Windows 真实 PID 不同，
# 所以一律按端口/命令行反查真实 PID，停进程用 taskkill（Windows）或 kill（类 Unix）。
HAS_TASKKILL=0; command -v taskkill >/dev/null 2>&1 && HAS_TASKKILL=1

port_pid() {
  netstat -ano 2>/dev/null | grep -E "[:.]$PORT[[:space:]]+.*LISTENING" | awk '{print $NF}' | head -1
}

# 全部 cloudflared 进程（Windows 用 tasklist，类 Unix 用 pgrep）
cloudflared_pids() {
  if [ "$HAS_TASKKILL" = "1" ]; then
    tasklist //FI "IMAGENAME eq cloudflared.exe" //FO CSV //NH 2>/dev/null \
      | cut -d, -f2 | tr -d '"' | tr -d '\r' | grep -E '^[0-9]+$'
  else
    pgrep -x cloudflared 2>/dev/null
  fi
}

# 本项目隧道的 PID：优先 pid 文件；否则仅在只有一个 cloudflared 实例时才认
tunnel_pid() {
  local pid
  if [ -f "$TUN_PID_FILE" ]; then
    pid="$(cat "$TUN_PID_FILE" 2>/dev/null)"
    if pid_alive "$pid"; then echo "$pid"; return 0; fi
  fi
  local all n
  all="$(cloudflared_pids)"
  n="$(echo "$all" | grep -c . 2>/dev/null || echo 0)"
  [ "$n" = "1" ] && echo "$all" | head -1
}

pid_alive() {
  local pid="$1"
  [ -n "$pid" ] || return 1
  if [ "$HAS_TASKKILL" = "1" ]; then
    tasklist //FI "PID eq $pid" //NH 2>/dev/null | grep -q "\b$pid\b"
  else
    kill -0 "$pid" 2>/dev/null
  fi
}

stop_pid() {
  local pid="$1" label="$2"
  [ -n "$pid" ] || return 0
  pid_alive "$pid" || return 0
  echo "停止 $label (pid $pid)"
  if [ "$HAS_TASKKILL" = "1" ]; then
    taskkill //F //PID "$pid" >/dev/null 2>&1
  else
    kill "$pid" 2>/dev/null; sleep 0.5; kill -9 "$pid" 2>/dev/null
  fi
  for _ in $(seq 1 10); do pid_alive "$pid" || return 0; sleep 0.3; done
  echo "警告：$label (pid $pid) 未能停止，请手动结束该进程。"
}

stop_all() {
  local pid
  for f in "$TUN_PID_FILE" "$SVC_PID_FILE"; do
    [ -f "$f" ] || continue
    stop_pid "$(cat "$f" 2>/dev/null)" "$(basename "$f" .pid)"
    rm -f "$f"
  done
  # pid 文件缺失/失配时按端口与命令行兜底，避免留下孤儿进程占端口
  pid="$(tunnel_pid)"; stop_pid "$pid" "tunnel(兜底)"
  pid="$(port_pid)";   stop_pid "$pid" "service(兜底)"
}

# 服务是否已在跑：有口令时 /api/meta 返回 401，同样说明服务活着
svc_up() {
  local code
  code="$(curl -s --noproxy '*' -m 3 -o /dev/null -w '%{http_code}' "$LOCAL/api/meta" 2>/dev/null)"
  [ "$code" = "200" ] || [ "$code" = "401" ]
}

# 等待真实 PID 出现并落盘（最多 10 秒）
wait_pid_file() {
  local file="$1" resolver="$2" pid=""
  for _ in $(seq 1 20); do
    pid="$($resolver)"
    if [ -n "$pid" ]; then echo "$pid" >"$file"; return 0; fi
    sleep 0.5
  done
  return 1
}

if [ "$MODE" = "stop" ]; then stop_all; echo "已停止。"; exit 0; fi
[ "$MODE" = "restart" ] && stop_all

# ---------- 1. 按需编译 ----------
needs_build=0
if [ ! -f "$BIN" ]; then
  needs_build=1
elif [ -n "$(find "$ROOT/src/backend" -name '*.go' -newer "$BIN" -print -quit 2>/dev/null)" ]; then
  needs_build=1
fi
if [ "$needs_build" = "1" ]; then
  echo "[1/3] 编译后端 ..."
  if ! ( cd "$ROOT" && go -C src/backend build -o "$BIN" . ); then
    echo "编译失败，已中止（数据库未被改动）。"; exit 1
  fi
else
  echo "[1/3] 后端已是最新，跳过编译"
fi

# ---------- 2. 启动服务 ----------
if svc_up; then
  pid="$(port_pid)"
  [ -n "$pid" ] && echo "$pid" >"$SVC_PID_FILE"
  echo "[2/3] 服务已在运行 (pid ${pid:-未知})，复用"
else
  echo "[2/3] 启动服务 $HOST:$PORT ..."
  ( cd "$ROOT" && "$BIN" >>"$LOGDIR/service.log" 2>&1 & )
  if ! wait_pid_file "$SVC_PID_FILE" port_pid; then
    echo "警告：未能确认服务 PID，--stop 将依赖端口反查。"
  fi
  for _ in $(seq 1 40); do svc_up && break; sleep 0.5; done
  if ! svc_up; then
    echo "服务启动失败，最后 20 行日志："
    tail -20 "$LOGDIR/service.log" 2>/dev/null
    exit 1
  fi
fi

# ---------- 3. Cloudflare 隧道 ----------
PUBLIC_URL=""
tunnel_url_from_log() { grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$LOGDIR/tunnel.log" 2>/dev/null | tail -1; }

# 公网是否真的可达（200/401 都算通；域名被回收或超时会得到 000）
tunnel_reachable() {
  local url="$1" code
  [ -n "$url" ] || return 1
  code="$(curl -s --noproxy '*' -m 8 -o /dev/null -w '%{http_code}' "$url/api/meta" 2>/dev/null)"
  case "$code" in "" | 000) return 1 ;; *) return 0 ;; esac
}

# 起一次 cloudflared，等域名分配并确认公网真的可达。
# 为什么要确认：quick tunnel 偶发「域名分到了但 DNS 没建起来」——日志里
# connection registered 一切正常，nslookup 却是 NXDOMAIN，等多久都不会好，
# 只能杀掉重开换一个新域名。不自动重试的话，用户会看到「隧道起来了但打不开」。
try_tunnel_once() {
  local before p
  PUBLIC_URL=""
  : >"$LOGDIR/tunnel.log"
  before="$(cloudflared_pids)"
  # 本机 UDP 7844 常被网络阻断，QUIC 起不来会返回 530，固定使用 HTTP/2。
  # 终端里的 HTTP(S)_PROXY 会被 cloudflared 继承，而代理通常不放行 7844，
  # 表现是 "dial tcp <edge>:7844: i/o timeout"，所以显式清掉代理变量直连边缘。
  ( cd "$ROOT" && env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    -u http_proxy -u https_proxy -u all_proxy \
    cloudflared tunnel --url "$LOCAL" --protocol http2 --no-autoupdate >>"$LOGDIR/tunnel.log" 2>&1 & )

  rm -f "$TUN_PID_FILE"
  for _ in $(seq 1 20); do
    for p in $(cloudflared_pids); do
      if ! echo "$before" | grep -qx "$p"; then echo "$p" >"$TUN_PID_FILE"; break 2; fi
    done
    sleep 0.5
  done

  for _ in $(seq 1 45); do
    PUBLIC_URL="$(tunnel_url_from_log)"
    [ -n "$PUBLIC_URL" ] && break
    sleep 1
  done
  [ -n "$PUBLIC_URL" ] || { echo "  隧道没有分配域名"; return 1; }

  # 域名刚落地时 DNS 可能还没生效，给它 20 秒
  for _ in $(seq 1 10); do
    tunnel_reachable "$PUBLIC_URL" && return 0
    sleep 2
  done
  return 1
}

start_tunnel() {
  echo "[3/3] 建立 Cloudflare 隧道 ..."
  local attempt pid
  for attempt in 1 2 3; do
    if try_tunnel_once; then
      [ -f "$TUN_PID_FILE" ] || echo "  警告：未能确认隧道 PID（--stop 会按实例数兜底）。"
      return 0
    fi
    echo "  第 $attempt 次尝试失败（域名 ${PUBLIC_URL:-未分配} 不可达，通常是 DNS 没建起来），换新域名重试 ..."
    pid="$(cat "$TUN_PID_FILE" 2>/dev/null)"
    [ -n "$pid" ] && stop_pid "$pid" "tunnel(不可达)"
    rm -f "$TUN_PID_FILE"
  done
  echo "隧道三次都没起来，最后 15 行日志（常见原因：7844 被墙、代理未清、或网络不通）："
  tail -15 "$LOGDIR/tunnel.log" 2>/dev/null
  echo "服务本身仍在局域网可用。"
  PUBLIC_URL=""
}

# 启动时把当前隧道地址上报一次，让固定入口立刻可用。
# 后续的心跳与「隧道换域名自动改报」由服务进程自己做（src/backend/reporter.go），
# 这样即使关掉这个终端，上报也不会停。
# 没配 GATEWAY_URL 就静默跳过：没有网关时系统照常在局域网/临时域名下工作。
report_gateway() {
  local payload code
  [ -n "$PUBLIC_URL" ] || return 0
  [ -n "${GW_URL:-}" ] || return 0
  payload="$(printf '{"name":"%s","prefix":"%s","target":"%s"}' "$GW_NAME" "$GW_PREFIX" "$PUBLIC_URL")"
  # GATEWAY_URL 可能带查询串（WorkBuddy 发布的分享链接带 sharecode），
  # 拼接口地址时要把查询串接到后面，而不是插在路径中间。
  local base="${GW_URL%%\?*}" query=""
  case "$GW_URL" in *\?*) query="?${GW_URL#*\?}" ;; esac
  # 不要加 --noproxy：上报目标是公网，需要走用户环境里的代理（若有）。
  local args=(-s -m 15 -X POST "${base%/}/api/register${query}"
    -H "Content-Type: application/json" -d "$payload" -o /dev/null -w '%{http_code}')
  [ -n "${GW_TOKEN:-}" ] && args+=(-H "X-GW-Token: $GW_TOKEN")
  code="$(curl "${args[@]}" 2>/dev/null)"
  case "$code" in
    200) echo "[4/4] 已上报网关：${GW_URL%%\?*}/$GW_PREFIX/（服务进程会每分钟续报）" ;;
    ""|000) echo "[4/4] 网关不可达（${GW_URL%%\?*}），固定入口暂时没更新；本机与局域网不受影响" ;;
    *) echo "[4/4] 网关拒绝上报（HTTP $code），请检查 .env 的 GATEWAY_TOKEN" ;;
  esac
}

if [ "$MODE" = "local" ]; then
  echo "[3/3] 跳过隧道（--local）"
elif ! command -v cloudflared >/dev/null 2>&1; then
  echo "[3/3] 未安装 cloudflared，跳过隧道。安装：winget install --id Cloudflare.cloudflared"
elif [ -n "$(tunnel_pid)" ]; then
  # 进程活着不等于隧道活着：边缘失联后进程会一直重试，公网域名也会被回收
  PUBLIC_URL="$(tunnel_url_from_log)"
  if tunnel_reachable "$PUBLIC_URL"; then
    echo "[3/3] 隧道已在运行 (pid $(tunnel_pid))，公网可达"
  else
    echo "[3/3] 隧道进程在 (pid $(tunnel_pid))，但公网 ${PUBLIC_URL:-无域名} 不可达，重启隧道"
    stop_pid "$(tunnel_pid)" "tunnel(失联)"
    rm -f "$TUN_PID_FILE"
    start_tunnel
  fi
else
  start_tunnel
fi

# ---------- 4. 上报网关 ----------
report_gateway

# ---------- 汇总 ----------
echo
echo "================ 访问信息 ================"
echo "本机:     $LOCAL"
# 只取本机 IPv4（ipconfig 输出是本地编码，需 -a 才能按文本处理）；取不到再退回通用匹配
ip_list="$(ipconfig 2>/dev/null | grep -a -i 'IPv4' | grep -oE '([0-9]{1,3}\.){3}[0-9]{1,3}' | grep -vE '^(127\.|255\.)' | sort -u | head -4)"
[ -z "$ip_list" ] && ip_list="$( (ipconfig 2>/dev/null || ifconfig 2>/dev/null) | grep -oE '([0-9]{1,3}\.){3}[0-9]{1,3}' | grep -vE '^(127\.|255\.)' | sort -u | head -4 )"
for ip in $ip_list; do echo "局域网:   http://$ip:$PORT"; done
if [ -n "$PUBLIC_URL" ]; then
  echo "公网:     $PUBLIC_URL"
  echo "手机:     $PUBLIC_URL/mobile.html （只有拍照上传）"
  echo "注意:     临时域名，cloudflared 重启后会变"
  [ -n "$GW_URL" ] && echo "固定入口: ${GW_URL%%\?*}/$GW_PREFIX/"
fi
if [ -n "$TOKEN" ]; then
  echo "口令:     $TOKEN"
else
  echo "口令:     未设置（.env 里没有 ACCESS_TOKEN，公网暴露前请务必设置）"
fi
echo "日志:     logs/service.log / logs/tunnel.log"
echo "停止:     bash scripts/start.sh --stop"
echo "=========================================="

# 前台驻留：Ctrl+C 同时停掉服务与隧道
trap 'echo; echo "收到中断，停止服务与隧道 ..."; stop_all; exit 0' INT TERM

# 隧道域名变化与心跳重报由服务进程自己负责（src/backend/reporter.go，默认每 60 秒一次），
# 好处是关掉这个终端、SSH 断开之后上报仍然继续。脚本只负责启动时立刻报一次，
# 让固定入口不用等服务的第一轮心跳。
while svc_up; do sleep 5; done
echo "服务已退出。"

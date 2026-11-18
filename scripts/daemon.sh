#!/usr/bin/env bash
# 家庭财务系统 · 常驻托管控制台（Windows 计划任务）
#
#   bash scripts/daemon.sh status    看任务状态、进程、当前隧道域名
#   bash scripts/daemon.sh start     启动服务与隧道守护（关掉终端也不会停）
#   bash scripts/daemon.sh stop      停止（写 stop.flag，守护进程不再拉起）
#   bash scripts/daemon.sh restart   先停再启
#   bash scripts/daemon.sh install   注册计划任务（只需一次，已注册会自动跳过）
#   bash scripts/daemon.sh logs      跟看守护/服务日志
#
# 原理：计划任务运行 finance-daemon/*.cmd → bash → scripts/supervisor.sh，
# 进程归 Windows 任务计划所有，不依附任何终端会话，因此不会被回收。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/scripts/bin"
LOGDIR="$ROOT/logs"
STOP_FLAG="$BIN/stop.flag"
WRAPPER_DIR="C:/Users/WangXin/WorkBuddy/finance-daemon"
TASKS="FinanceService FinanceTunnel"

say() { printf '%s\n' "$*"; }

have_schtasks() { command -v schtasks >/dev/null 2>&1; }

task_exists() { have_schtasks && schtasks //query //tn "$1" >/dev/null 2>&1; }

install_tasks() {
  have_schtasks || { say "没有 schtasks，此脚本仅适用于 Windows。"; exit 1; }
  for t in service tunnel; do
    [ -f "$WRAPPER_DIR/$t.cmd" ] || {
      say "缺少入口脚本：$WRAPPER_DIR/$t.cmd"
      say "该目录是纯 ASCII 路径，用来避开中文路径在计划任务命令行里的编码问题。"
      exit 1
    }
  done
  local n=0
  for pair in "FinanceService:service" "FinanceTunnel:tunnel"; do
    task="${pair%%:*}"; cmd="${pair##*:}"
    if task_exists "$task"; then say "已存在，跳过：$task"; continue; fi
    # /IT：只在当前用户登录时运行，无需保存密码；/SC ONCE 只是占位，靠 /run 启动
    schtasks //create //tn "$task" \
      //tr "C:\\Users\\WangXin\\WorkBuddy\\finance-daemon\\$cmd.cmd" \
      //sc ONCE //st 00:00 //it //f >/dev/null 2>&1 \
      && { say "已注册：$task"; n=$((n + 1)); } || say "注册失败：$task（可能被安全策略拦）"
  done
  say "完成，共注册 $n 个。接下来执行：bash scripts/daemon.sh start"
}

start_tasks() {
  rm -f "$STOP_FLAG"
  have_schtasks || { say "没有 schtasks"; exit 1; }
  for t in $TASKS; do
    task_exists "$t" || { say "任务不存在：$t（先 bash scripts/daemon.sh install）"; exit 1; }
    schtasks //run //tn "$t" >/dev/null 2>&1 && say "已启动：$t" || say "启动失败：$t"
  done
  # 服务要先占住端口，隧道才连得上
  for _ in $(seq 1 30); do
    code="$(curl -s --noproxy '*' -m 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8081/api/meta 2>/dev/null)"
    [ "$code" != "000" ] && { say "本机服务就绪（HTTP $code，401 表示口令保护生效）"; break; }
    sleep 1
  done
  say "隧道约 10~20 秒后拿到域名，之后固定入口自动生效。"
}

stop_tasks() {
  touch "$STOP_FLAG"
  have_schtasks && for t in $TASKS; do
    task_exists "$t" && schtasks //end //tn "$t" >/dev/null 2>&1
  done
  sleep 1
  taskkill //F //IM finance.exe >/dev/null 2>&1
  taskkill //F //IM cloudflared.exe >/dev/null 2>&1
  say "已停止（保留 stop.flag，守护进程不会自动拉起）。"
}

status() {
  say "=== 计划任务 ==="
  if have_schtasks; then
    # 任务状态的中文输出在 Git Bash 里是 GBK 乱码，所以只看注册与否，
    # 实际是否在跑由下面的进程数反映。
    for t in $TASKS; do
      task_exists "$t" && say "  $t: 已注册" || say "  $t: 未注册（bash scripts/daemon.sh install）"
    done
  else
    say "  没有 schtasks"
  fi

  say "=== 进程 ==="
  p="$(tasklist 2>/dev/null | grep -icE '^finance\.exe')"
  c="$(tasklist 2>/dev/null | grep -icE '^cloudflared\.exe')"
  say "  finance.exe: ${p:-0} 个    cloudflared.exe: ${c:-0} 个"

  say "=== 本机服务 ==="
  code="$(curl -s --noproxy '*' -m 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8081/api/meta 2>/dev/null)"
  case "$code" in
    401|200) say "  http://127.0.0.1:8081 正常（HTTP $code）" ;;
    000)     say "  打不开（服务没起来）" ;;
    *)       say "  HTTP $code" ;;
  esac

  say "=== 隧道 ==="
  url="$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$LOGDIR/tunnel.log" 2>/dev/null | tail -1)"
  say "  ${url:-未分配}"
  [ -n "$url" ] && {
    tc="$(curl -s -m 8 -o /dev/null -w '%{http_code}' "$url/api/meta" 2>/dev/null)"
    say "  公网直连: HTTP $tc$([ "$tc" = "000" ] && echo '（域名没解析或隧道已断）')"
  }

  say "=== 最近守护日志 ==="
  tail -4 "$LOGDIR/daemon.log" 2>/dev/null | sed 's/^/  /'
  say "=== 最近上报 ==="
  grep -E '固定入口' "$LOGDIR/service.log" 2>/dev/null | tail -2 | sed 's/^/  /'
}

case "${1:-status}" in
  install) install_tasks ;;
  start)   start_tasks ;;
  stop)    stop_tasks ;;
  restart) stop_tasks; sleep 2; start_tasks ;;
  status)  status ;;
  logs)    tail -f "$LOGDIR/daemon.log" "$LOGDIR/service.log" ;;
  *) echo "用法: $0 status|start|stop|restart|install|logs"; exit 1 ;;
esac

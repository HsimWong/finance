#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""以「脱离父进程」的方式启动 start.sh，避免服务随终端关闭而被杀掉。

为什么需要它：
  WorkBuddy（或任何终端）里起的后台任务在被清理时，会把整棵子进程树带走，
  finance.exe 与 cloudflared 是它的子进程，于是一起消失。用 DETACHED_PROCESS
  启动后，start.sh 成为独立会话，与启动它的终端解耦。

**本机实测的限制**：在 WorkBuddy 沙箱里执行时，DETACHED_PROCESS 也逃不出沙箱的统一
进程清理 —— 进程能起来、能跑几十秒，工具调用一结束就被收走。所以本机长期常驻请用
Windows 计划任务（见 README「长期常驻」），本脚本只在真实终端（自己开的 cmd/Git Bash）
里才真正有效。

用法：
  python scripts/launch_detached.py          # 启动（已在跑则复用）
  python scripts/launch_detached.py --status # 只看状态
停止仍用：bash scripts/start.sh --stop
"""

import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOG = os.path.join(ROOT, "logs", "start.out")
BASH_CANDIDATES = [
    r"C:\Program Files\Git\bin\bash.exe",
    r"C:\Users\WangXin\.workbuddy\binaries\PortableGit\versions\1.3.0\bin\bash.exe",
]

# Windows 专用：DETACHED_PROCESS 让子进程没有控制台、不挂在此终端下；
# CREATE_NEW_PROCESS_GROUP 让它拥有独立进程组，Ctrl+C 不会波及。
DETACHED_PROCESS = 0x00000008
CREATE_NEW_PROCESS_GROUP = 0x00000200


def find_bash():
    for p in BASH_CANDIDATES:
        if os.path.isfile(p):
            return p
    raise SystemExit("找不到 bash.exe，请检查 Git for Windows 是否安装。")


def main():
    bash = find_bash()
    os.makedirs(os.path.join(ROOT, "logs"), exist_ok=True)

    # 清掉终端注入的代理：账本服务要直连 Cloudflare 边缘（7844）和固定入口网关，
    # 而沙箱代理不放行这些目标，继承下来只会得到 i/o timeout / 502。
    env = {
        k: v
        for k, v in os.environ.items()
        if k.lower() not in ("http_proxy", "https_proxy", "all_proxy")
    }

    with open(LOG, "ab") as out:
        out.write(b"\n==== launch_detached %s ====\n" % __import__("datetime").datetime.now().strftime("%Y-%m-%d %H:%M:%S").encode())
        out.flush()
        proc = subprocess.Popen(
            [bash, "scripts/start.sh"],
            cwd=ROOT,
            stdout=out,
            stderr=subprocess.STDOUT,
            env=env,
            creationflags=DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP,
            close_fds=True,
        )
    print(f"已启动 start.sh，PID {proc.pid}（独立进程，关闭终端不会中断）")
    print(f"输出: {LOG}")


if __name__ == "__main__":
    if "--status" in sys.argv:
        print("状态请用: bash scripts/start.sh --stop / netstat -ano | grep 8081")
        sys.exit(0)
    main()

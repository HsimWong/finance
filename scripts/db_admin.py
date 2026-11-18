#!/usr/bin/env python3
"""家庭财务数据库管理工具：备份 / 校验 / 回档 / 清空 / 查看。

重要约定
--------
本项目数据库以 WAL 模式运行，数据常常还在 finance.db-wal 里。
**绝对不要直接复制 finance.db 当作备份** —— 那样会丢掉尚未 checkpoint 的数据。
本工具一律使用 SQLite 在线备份 API（Connection.backup），得到的是包含 WAL
已提交数据的单文件一致性快照。

任何涉及数据库结构或批量数据的变更，第一步永远是：

    python scripts/db_admin.py backup --label before-xxx

常用命令
--------
    python scripts/db_admin.py info                     # 当前各表行数 / WAL 状态
    python scripts/db_admin.py list                    # 列出全部备份
    python scripts/db_admin.py backup --label before-xxx  # 备份（只保留最新一份）
    python scripts/db_admin.py verify <备份.db>         # 完整性 + 哈希校验
    python scripts/db_admin.py reset --yes --force-with-data  # 清空业务数据（真实数据需二次确认）
    python scripts/db_admin.py restore <备份.db> --yes # 备份现状后回档（须先停服务）
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import sys
import urllib.request

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")

ROOT = Path(__file__).resolve().parent.parent
DB = ROOT / "data" / "finance.db"
BACKUP_DIR = ROOT / "data" / "backups"
SERVICE_URL = "http://127.0.0.1:8081/api/meta"

# 业务数据表：清空时删除。members / categories / tags 属于基础配置，保留。
BUSINESS_TABLES = [
    "stock_movements",
    "stock_items",
    "entry_tags",
    "entries",
    "allocations",
    "screenshots",
]
KEEP_TABLES = ["members", "categories", "tags"]


def table_names(con: sqlite3.Connection) -> list[str]:
    return [
        r[0]
        for r in con.execute(
            "SELECT name FROM sqlite_master WHERE type='table' "
            "AND name NOT LIKE 'sqlite_%' ORDER BY name"
        )
    ]


def row_counts(con: sqlite3.Connection) -> dict[str, int]:
    return {t: con.execute(f'SELECT COUNT(*) FROM "{t}"').fetchone()[0] for t in table_names(con)}


def sha16(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()[:16]


def service_running() -> bool:
    try:
        with urllib.request.urlopen(SERVICE_URL, timeout=1.5):
            return True
    except Exception:
        return False


def require_service_stopped(allow_running: bool) -> None:
    if allow_running:
        return
    if service_running():
        sys.exit(
            "错误：服务仍在运行（8081）。清空 / 回档前请先停止服务，"
            "否则 WAL 中的写入可能覆盖操作结果。\n"
            "确知风险时可加 --allow-running，但不建议。"
        )


def do_backup(label: str, protect: Path | None = None) -> Path:
    if not DB.exists():
        sys.exit(f"错误：数据库不存在 {DB}")
    BACKUP_DIR.mkdir(parents=True, exist_ok=True)
    stamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    safe = "".join(c if c.isalnum() or c in "-_" else "-" for c in label).strip("-") or "manual"
    base = f"{stamp}_{safe}"
    out = BACKUP_DIR / f"{base}.db"
    seq = 1
    while out.exists():
        base = f"{stamp}_{safe}-{seq}"
        out = BACKUP_DIR / f"{base}.db"
        seq += 1

    con = sqlite3.connect(DB)
    try:
        # 让 WAL 里的内容合并进主文件，再做在线备份（双保险）
        con.execute("PRAGMA wal_checkpoint(PASSIVE)")
        target = sqlite3.connect(out)
        try:
            con.backup(target)
        finally:
            target.close()
        meta = {
            "created_at": dt.datetime.now().isoformat(timespec="seconds"),
            "label": label,
            "source": "data/finance.db",
            "backup": f"data/backups/{base}.db",
            "size_bytes": out.stat().st_size,
            "sha256_16": sha16(out),
            "integrity_check": con.execute("PRAGMA integrity_check").fetchone()[0],
            "user_version": con.execute("PRAGMA user_version").fetchone()[0],
            "table_rows": row_counts(con),
        }
    finally:
        con.close()
    (BACKUP_DIR / f"{base}.json").write_text(
        json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    print(f"备份完成: data/backups/{base}.db  ({meta['size_bytes']} bytes, sha256:{meta['sha256_16']})")
    print(f"完整性: {meta['integrity_check']} | 各表行数: {json.dumps(meta['table_rows'], ensure_ascii=False)}")
    prune_keep_latest(out, protect=protect)
    return out


def prune_keep_latest(keep: Path, protect: Path | None = None) -> None:
    """只保留最新一份备份（.db + .json），其余全部删除以节省存储。

    用户规则（2026-11-18）：always keep the latest backup, abort others。
    protect 用于回档场景：回档来源在被拷贝完成前不能被删。
    同时清理没有对应 .db 的孤立 -wal / -shm 残留。
    """
    keep_names = {keep.name, keep.with_suffix(".json").name}
    if protect is not None:
        keep_names.add(protect.name)
        keep_names.add(protect.with_suffix(".json").name)
    for p in BACKUP_DIR.iterdir():
        if p.name in keep_names:
            continue
        if p.suffix in (".db", ".json") or p.name.endswith((".db-wal", ".db-shm")):
            p.unlink(missing_ok=True)


def do_info() -> None:
    if not DB.exists():
        print("数据库尚未创建。启动一次服务即可。")
        return
    con = sqlite3.connect(DB)
    try:
        counts = row_counts(con)
        print(f"数据库: data/finance.db  ({DB.stat().st_size} bytes)")
        for suffix in ("-wal", "-shm"):
            p = Path(str(DB) + suffix)
            if p.exists():
                print(f"WAL 附属文件: {p.name}  ({p.stat().st_size} bytes)")
        print(f"integrity_check: {con.execute('PRAGMA integrity_check').fetchone()[0]}")
        print(f"user_version: {con.execute('PRAGMA user_version').fetchone()[0]}")
        print(f"journal_mode: {con.execute('PRAGMA journal_mode').fetchone()[0]}")
        print("各表行数:")
        for t, n in counts.items():
            mark = "（基础配置，清空时保留）" if t in KEEP_TABLES else ""
            print(f"  {t:<18} {n:>6} {mark}")
    finally:
        con.close()


def do_list() -> None:
    metas = sorted(BACKUP_DIR.glob("*.json"), key=lambda p: p.name, reverse=True)
    if not metas:
        print("暂无备份。执行：python scripts/db_admin.py backup --label 名称")
        return
    print(f"备份目录: data/backups/  共 {len(metas)} 份")
    print(f"{'备份文件':<46} {'标签':<22} {'大小':>10}  行数摘要")
    for m in metas:
        d = json.loads(m.read_text(encoding="utf-8"))
        rows = d.get("table_rows", {})
        summary = f"流水 {rows.get('entries', '?')} / 截图 {rows.get('screenshots', '?')} / 库存 {rows.get('stock_items', '?')}"
        print(f"{m.with_suffix('.db').name:<46} {str(d.get('label', '')):<22} {d.get('size_bytes', 0):>10}  {summary}")


def do_verify(path: Path) -> None:
    if not path.exists():
        sys.exit(f"错误：备份不存在 {path}")
    meta_path = path.with_suffix(".json")
    con = sqlite3.connect(f"file:{path.as_posix()}?mode=ro", uri=True)
    try:
        ok = con.execute("PRAGMA integrity_check").fetchone()[0]
        counts = row_counts(con)
    finally:
        con.close()
    print(f"备份: {path.name}")
    print(f"integrity_check: {ok}")
    print(f"sha256(前16): {sha16(path)}")
    if meta_path.exists():
        d = json.loads(meta_path.read_text(encoding="utf-8"))
        print("元数据记录:", d.get("created_at"), "| 标签:", d.get("label"))
        print("哈希一致:", "是" if d.get("sha256_16") == sha16(path) else "否（文件被改动过！）")
        print("行数一致:", "是" if d.get("table_rows") == counts else "否（文件被改动过！）")
    print("各表行数:", json.dumps(counts, ensure_ascii=False))
    if ok != "ok":
        sys.exit("校验失败：备份不完整")


def do_reset(yes: bool, allow_running: bool, force_with_data: bool) -> None:
    if not yes:
        sys.exit("这是清空业务数据的操作。确认无误请加 --yes（会先自动备份）。")
    if not DB.exists():
        sys.exit("数据库不存在，无需清空。")
    require_service_stopped(allow_running)
    # 真实数据闸门：库里已有业务数据时，必须显式确认已备份且可恢复
    probe = sqlite3.connect(f"file:{DB.as_posix()}?mode=ro", uri=True)
    try:
        n = sum(
            probe.execute(f'SELECT COUNT(*) FROM "{t}"').fetchone()[0] for t in BUSINESS_TABLES
        )
    finally:
        probe.close()
    if n > 0 and not force_with_data:
        sys.exit(
            f"当前库含 {n} 行业务数据（试用阶段视为真实数据），reset 会清空它们。\n"
            "按数据保护约定：请先执行 `backup --label before-reset` 并 `verify` 确认可恢复，\n"
            "确认无误后再加 --force-with-data 执行。此命令已中止，未改动任何数据。"
        )
    print(f"将清空 {len(BUSINESS_TABLES)} 张业务表: {', '.join(BUSINESS_TABLES)}")
    print(f"将保留基础配置: {', '.join(KEEP_TABLES)}")
    do_backup("before-reset")

    con = sqlite3.connect(DB)
    try:
        con.execute("PRAGMA foreign_keys=ON")
        con.execute("PRAGMA wal_checkpoint(TRUNCATE)")
        with con:
            for t in BUSINESS_TABLES:
                con.execute(f'DELETE FROM "{t}"')
            has_seq = con.execute(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name='sqlite_sequence'"
            ).fetchone()
            if has_seq:
                placeholders = ",".join("?" for _ in BUSINESS_TABLES)
                con.execute(
                    f"DELETE FROM sqlite_sequence WHERE name IN ({placeholders})", BUSINESS_TABLES
                )
        con.execute("VACUUM")
        con.execute("PRAGMA wal_checkpoint(TRUNCATE)")
        print("清空完成，各表行数:", json.dumps(row_counts(con), ensure_ascii=False))
    finally:
        con.close()
    print("提示：重启服务后可开始试用；清空前的完整数据仍在 data/backups/。")


def do_restore(path: Path, yes: bool, allow_running: bool) -> None:
    if not yes:
        sys.exit("回档会覆盖当前数据库。确认无误请加 --yes（会先自动备份现状）。")
    if not path.exists():
        sys.exit(f"错误：备份不存在 {path}")
    require_service_stopped(allow_running)
    # 先校验备份可用，再动现状
    con = sqlite3.connect(f"file:{path.as_posix()}?mode=ro", uri=True)
    try:
        if con.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
            sys.exit("备份完整性校验失败，已中止，未改动当前数据库")
    finally:
        con.close()
    if DB.exists():
        # 保护待回档的源备份，避免被「只留最新」清理掉导致拷贝到一半文件消失
        do_backup("before-restore", protect=path)
    BACKUP_DIR.mkdir(parents=True, exist_ok=True)
    shutil.copy2(path, DB)
    # 旧 WAL / SHM 必须删除，否则会覆盖回档进来的数据
    for suffix in ("-wal", "-shm"):
        p = Path(str(DB) + suffix)
        if p.exists():
            p.unlink()
    con = sqlite3.connect(DB)
    try:
        print("回档完成:", json.dumps(row_counts(con), ensure_ascii=False))
        print("integrity_check:", con.execute("PRAGMA integrity_check").fetchone()[0])
    finally:
        con.close()
    # 回档成功后收敛：保留最新的 before-restore 快照（回档前的现状），删掉受保护的来源
    newest = max(BACKUP_DIR.glob("*.db"), key=lambda p: p.name, default=None)
    if newest is not None:
        prune_keep_latest(newest)


def main() -> None:
    ap = argparse.ArgumentParser(description="家庭财务数据库备份 / 回档 / 清空工具")
    sub = ap.add_subparsers(dest="cmd", required=True)

    sub.add_parser("info", help="查看当前数据库状态与各表行数")
    sub.add_parser("list", help="列出全部备份")

    b = sub.add_parser("backup", help="生成一致性备份")
    b.add_argument("--label", default="manual", help="备份标签，如 before-migration")

    v = sub.add_parser("verify", help="校验备份完整性与哈希")
    v.add_argument("file", type=Path)

    r = sub.add_parser("reset", help="备份后清空业务数据（保留基础配置）")
    r.add_argument("--yes", action="store_true")
    r.add_argument("--force-with-data", action="store_true", help="库内有业务数据时必须显式确认（须先备份并 verify）")
    r.add_argument("--allow-running", action="store_true")

    s = sub.add_parser("restore", help="备份现状后从指定备份回档")
    s.add_argument("file", type=Path)
    s.add_argument("--yes", action="store_true")
    s.add_argument("--allow-running", action="store_true")

    a = ap.parse_args()
    if a.cmd == "info":
        do_info()
    elif a.cmd == "list":
        do_list()
    elif a.cmd == "backup":
        do_backup(a.label)
    elif a.cmd == "verify":
        do_verify(a.file)
    elif a.cmd == "reset":
        do_reset(a.yes, a.allow_running, a.force_with_data)
    elif a.cmd == "restore":
        do_restore(a.file, a.yes, a.allow_running)


if __name__ == "__main__":
    main()

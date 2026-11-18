package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 这两个测试是「真实数据严格保护」的回归防线：
// ① WAL 模式下备份必须包含尚未 checkpoint 的数据；
// ② 服务长时间不重启时，数据一变化就要留下可恢复的快照。
// 只使用 t.TempDir()，不会碰 data/finance.db。

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	for _, s := range schemaStatements {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	if err := seed(d); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func insertTestEntry(t *testing.T, d *sql.DB, note string) {
	t.Helper()
	now := nowText()
	_, err := d.Exec(`INSERT INTO entries(type, amount_cents, occurred_at, source, note, created_at, updated_at)
		VALUES('expense', 1234, ?, 'manual', ?, ?, ?)`, now, note, now, now)
	if err != nil {
		t.Fatal(err)
	}
}

func countNote(t *testing.T, dbPath, note string) int {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM entries WHERE note = ?`, note).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestKeepOnlyLatestBackup 守住用户规则：备份目录始终只有最新一份，
// 旧的 .db/.json 与孤立 -wal/-shm 都被清掉。
func TestKeepOnlyLatestBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "finance.db")
	d := openTestDB(t, dbPath)
	defer d.Close()

	b1, err := backupDB(d, dbPath, "older")
	if err != nil {
		t.Fatal(err)
	}
	// 造一个旧的孤立附属文件，验证一并清理
	orphan := b1 + "-wal"
	if err := os.WriteFile(orphan, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	insertTestEntry(t, d, "第二份备份才有")
	b2, err := backupDB(d, dbPath, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if b1 == b2 {
		t.Fatal("两次备份路径不应相同")
	}
	if n := countNote(t, b2, "第二份备份才有"); n != 1 {
		t.Fatalf("最新备份应包含最新数据，count=%d", n)
	}
	dbs, _ := filepath.Glob(filepath.Join(backupDir(dbPath), "*.db"))
	if len(dbs) != 1 || dbs[0] != b2 {
		t.Fatalf("应只剩最新一份备份，实际: %v", dbs)
	}
	if _, err := os.Stat(b1); !os.IsNotExist(err) {
		t.Fatalf("旧备份未被删除: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("孤立 -wal 未被删除: %v", err)
	}
}

func TestBackupContainsWALData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "finance.db")
	d := openTestDB(t, dbPath)
	defer d.Close()

	insertTestEntry(t, d, "WAL-未落盘数据")
	b, err := backupDB(d, dbPath, "test-wal")
	if err != nil {
		t.Fatal(err)
	}
	if n := countNote(t, b, "WAL-未落盘数据"); n != 1 {
		t.Fatalf("备份缺少 WAL 中的数据，count=%d（裸拷贝主文件的典型症状）", n)
	}
}

func TestAutoSnapshotOnDataChange(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "finance.db")
	d := openTestDB(t, dbPath)
	defer d.Close()

	stop := startSnapshotLoop(d, dbPath, 150*time.Millisecond)
	defer stop()
	time.Sleep(350 * time.Millisecond) // 让循环取到初始指纹

	if files, _ := filepath.Glob(filepath.Join(backupDir(dbPath), "*_daily-*.db")); len(files) != 0 {
		t.Fatal("数据未变化时不应产生重复快照")
	}

	insertTestEntry(t, d, "真实数据-变更触发")
	var found string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		daily, _ := filepath.Glob(filepath.Join(backupDir(dbPath), "*_daily-*.db"))
		auto, _ := filepath.Glob(filepath.Join(backupDir(dbPath), "*_auto*.db"))
		all := append(daily, auto...)
		if len(all) > 0 {
			found = all[len(all)-1]
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if found == "" {
		t.Fatal("数据变化后未自动生成快照")
	}
	if n := countNote(t, found, "真实数据-变更触发"); n != 1 {
		t.Fatalf("自动快照未包含变更后的数据，count=%d", n)
	}
}

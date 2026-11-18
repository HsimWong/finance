package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 这个测试守住 v1 -> v2 的升级路径：
// 真实库从旧的分类树（交通›加油/停车费、保险›车险）升到新的「汽车」领域，
// 要求 ① 分类树正确 ② 已有流水的分类引用不丢 ③ 升级前一定先备份。
// 只用 t.TempDir()，不碰 data/finance.db。

var legacyCategories = []seedCategory{
	{"餐饮", "expense", []string{"外卖", "买菜生鲜"}},
	{"交通", "expense", []string{"公交地铁", "打车", "加油", "停车费"}},
	{"保险", "expense", []string{"车险", "人身险", "财产险"}},
	{"其他支出", "expense", nil},
	{"工资收入", "income", nil},
}

// seedLegacyV1 造一个 v1 结构的库：旧分类树 + 一条挂在「加油」上的历史流水。
func seedLegacyV1(t *testing.T, d *sql.DB) int64 {
	t.Helper()
	var fuelID int64
	for i, c := range legacyCategories {
		res, err := d.Exec(`INSERT INTO categories(parent_id, name, kind, sort_order) VALUES(NULL, ?, ?, ?)`, c.name, c.kind, i)
		if err != nil {
			t.Fatal(err)
		}
		pid, _ := res.LastInsertId()
		for j, child := range c.children {
			r, err := d.Exec(`INSERT INTO categories(parent_id, name, kind, sort_order) VALUES(?, ?, ?, ?)`, pid, child, c.kind, j)
			if err != nil {
				t.Fatal(err)
			}
			if child == "加油" {
				fuelID, _ = r.LastInsertId()
			}
		}
	}
	now := nowText()
	if _, err := d.Exec(`INSERT INTO members(name, created_at) VALUES('成员 A', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO tags(name) VALUES('周期固定')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO entries(type, amount_cents, occurred_at, category_id, source, note, created_at, updated_at)
		VALUES('expense', 30000, ?, ?, 'manual', '历史加油', ?, ?)`, now, fuelID, now, now); err != nil {
		t.Fatal(err)
	}
	return fuelID
}

func TestMigrationV2CarCategories(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "finance.db")

	// 造 v1 库
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	for _, s := range schemaStatements {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	fuelID := seedLegacyV1(t, raw)
	if _, err := raw.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	// 走正常启动流程：备份 -> 迁移
	d, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	defer d.Close()

	if v := currentUserVersion(d); v != schemaVersion {
		t.Fatalf("结构版本应升到 v%d，实际 v%d", schemaVersion, v)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "backups", fmt.Sprintf("*_before-migration-v%d.db", schemaVersion))); len(files) != 1 {
		t.Fatalf("升级前未生成 before-migration-v%d 备份，实际文件: %v", schemaVersion, files)
	}

	// ① 分类树
	children := func(parent string) []string {
		rows, err := d.Query(`SELECT c.name FROM categories c JOIN categories p ON p.id = c.parent_id
			WHERE p.parent_id IS NULL AND p.name = ? ORDER BY c.sort_order, c.id`, parent)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err == nil {
				out = append(out, n)
			}
		}
		return out
	}
	wantCar := []string{"加油", "停车费", "保养", "维修", "保险"}
	gotCar := children("汽车")
	if len(gotCar) != len(wantCar) {
		t.Fatalf("汽车 子分类 = %v，期望 %v", gotCar, wantCar)
	}
	for i := range wantCar {
		if gotCar[i] != wantCar[i] {
			t.Fatalf("汽车 子分类 = %v，期望 %v", gotCar, wantCar)
		}
	}
	if got := children("交通"); len(got) != 2 || got[0] != "公交地铁" || got[1] != "打车" {
		t.Fatalf("交通 应只剩 公交地铁/打车，实际 %v", got)
	}
	if got := children("保险"); len(got) != 2 || got[0] != "人身险" || got[1] != "财产险" {
		t.Fatalf("保险 应只剩 人身险/财产险，实际 %v", got)
	}

	// ② 历史流水的分类 id 不变，且已经落在 汽车›加油 下
	var gotFuelID int64
	var root, leaf string
	if err := d.QueryRow(`SELECT e.category_id, p.name, c.name FROM entries e
		JOIN categories c ON c.id = e.category_id JOIN categories p ON p.id = c.parent_id
		WHERE e.note = '历史加油'`).Scan(&gotFuelID, &root, &leaf); err != nil {
		t.Fatalf("历史流水的分类引用丢失: %v", err)
	}
	if gotFuelID != fuelID || root != "汽车" || leaf != "加油" {
		t.Fatalf("流水分类应为 id=%d 汽车›加油，实际 id=%d %s›%s", fuelID, gotFuelID, root, leaf)
	}

	// ③ 重复执行不应产生重复分类（幂等）
	if err := applyMigrations(d); err != nil {
		t.Fatalf("重复迁移报错: %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM categories WHERE name = '汽车' AND parent_id IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("汽车 顶级分类应唯一，实际 %d 个", n)
	}
}

// v2 -> v3 只修数据：把库存品类里「key 与 name 自相矛盾」的记录纠回正确名称。
// 真实场景：手工入库时选了「药品」，但物品名「别嘌醇缓释胶囊」不含「药」字，
// 显示名被写成「其他」，导致库里出现 key=medicine / name=其他。
func TestMigrationV3FixesStockCategoryName(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "finance.db")

	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	for _, s := range schemaStatements {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	now := nowText()
	seedRows := []struct{ name, key, catName string }{
		{"别嘌醇缓释胶囊", "medicine", "其他"}, // 选药品但名称被写错（本次要修的）
		{"可乐定缓释片", "other", "其他"},     // 当时没识别出品类
		{"咖啡胶囊", "other", "其他"},       // 含剂型词但非药品，必须保持不动
		{"鸡蛋", "meat_egg_milk", "其他"}, // 其他品类的同类脏数据
		{"猫罐头", "cat_food", "猫罐头"},    // 本来就正确的记录
	}
	for _, r := range seedRows {
		if _, err := raw.Exec(`INSERT INTO stock_items(name, category_key, category_name, unit, qty,
			amount_cents, expiry_date, status, auto_created, note, created_at, updated_at)
			VALUES(?, ?, ?, '件', 1, 0, '', 'in_stock', 0, '', ?, ?)`, r.name, r.key, r.catName, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	d, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	defer d.Close()

	if v := currentUserVersion(d); v != schemaVersion {
		t.Fatalf("结构版本应升到 v%d，实际 v%d", schemaVersion, v)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "backups", fmt.Sprintf("*_before-migration-v%d.db", schemaVersion))); len(files) != 1 {
		t.Fatalf("升级前未生成备份，实际文件: %v", files)
	}

	want := map[string][2]string{
		"别嘌醇缓释胶囊": {"medicine", "药品"},
		"可乐定缓释片":  {"medicine", "药品"},
		"咖啡胶囊":    {"other", "其他"},
		"鸡蛋":      {"meat_egg_milk", "肉蛋奶"},
		"猫罐头":     {"cat_food", "猫罐头"},
	}
	rows, err := d.Query(`SELECT name, category_key, category_name FROM stock_items`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, key, catName string
		if err := rows.Scan(&name, &key, &catName); err != nil {
			t.Fatal(err)
		}
		seen++
		w, ok := want[name]
		if !ok {
			t.Fatalf("出现未预期的库存项: %s", name)
		}
		if key != w[0] || catName != w[1] {
			t.Errorf("%s 升级后为 %s/%s，期望 %s/%s", name, key, catName, w[0], w[1])
		}
	}
	if seen != len(want) {
		t.Fatalf("库存项数量应保持 %d，实际 %d（迁移不能删数据）", len(want), seen)
	}
}

// 全新库直接初始化时，分类树应与升级后的结果一致（seed 与 migration 不能跑偏）。
func TestFreshDBHasCarCategories(t *testing.T) {
	dir := t.TempDir()
	d, err := openDB(filepath.Join(dir, "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.Query(`SELECT c.name FROM categories c JOIN categories p ON p.id = c.parent_id
		WHERE p.parent_id IS NULL AND p.name = '汽车' ORDER BY c.sort_order, c.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil {
			got = append(got, n)
		}
	}
	want := []string{"加油", "停车费", "保养", "维修", "保险"}
	if len(got) != len(want) {
		t.Fatalf("全新库 汽车 子分类 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("全新库 汽车 子分类 = %v，期望 %v", got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "finance.db")); err != nil {
		t.Fatalf("数据库文件未创建: %v", err)
	}
}

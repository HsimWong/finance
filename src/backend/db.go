package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	existed := statErr == nil // 已有库：任何 DDL 之前先留后路

	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 单进程自用，单连接避免写锁竞争
	d.SetMaxOpenConns(1)
	if err := d.Ping(); err != nil {
		return nil, err
	}

	// ① 结构变更前强制备份：版本落后时先备份；备份失败一律中止，绝不带着真实数据冒险
	if existed {
		if err := preSchemaGuard(d, path); err != nil {
			d.Close()
			return nil, err
		}
		// ② 启动快照：即使忘记手动备份，也总能回到最近一次启动时的完整状态
		startupSnapshot(d, path)
	}

	// ③ 幂等建表（对既有库不改动任何一行数据）
	for _, stmt := range schemaStatements {
		if _, err := d.Exec(stmt); err != nil {
			return nil, fmt.Errorf("建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	if err := seed(d); err != nil {
		return nil, err
	}
	// ④ 版本化迁移 + 写入结构版本号
	if err := applyMigrations(d); err != nil {
		return nil, err
	}
	return d, nil
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS members (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		parent_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'expense',
		sort_order INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS tags (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE
	)`,
	`CREATE TABLE IF NOT EXISTS screenshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		filename TEXT NOT NULL,
		mime TEXT NOT NULL,
		size INTEGER NOT NULL,
		data BLOB NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		note TEXT NOT NULL DEFAULT '',
		uploaded_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS allocations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		period TEXT NOT NULL,
		batch TEXT NOT NULL,
		name TEXT NOT NULL,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		budget_cents INTEGER NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL CHECK(type IN ('income','expense')),
		amount_cents INTEGER NOT NULL CHECK(amount_cents > 0),
		occurred_at TEXT NOT NULL,
		member_id INTEGER REFERENCES members(id) ON DELETE SET NULL,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		allocation_id INTEGER REFERENCES allocations(id) ON DELETE SET NULL,
		screenshot_id INTEGER REFERENCES screenshots(id) ON DELETE SET NULL,
		source TEXT NOT NULL DEFAULT 'manual',
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS entry_tags (
		entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
		tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
		PRIMARY KEY (entry_id, tag_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_occurred ON entries(occurred_at)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_member ON entries(member_id)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_category ON entries(category_id)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_allocation ON entries(allocation_id)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_screenshot ON entries(screenshot_id)`,
	`CREATE INDEX IF NOT EXISTS idx_allocations_period ON allocations(period)`,
	// 计划单（v4）：预算是多对多关联到计划单上的，不改动 allocations 表结构。
	// 流水挂到预算时，把含该预算的 active 计划单标记完成（clear），全部数据保留。
	`CREATE TABLE IF NOT EXISTS plans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		period TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','completed')),
		created_at TEXT NOT NULL,
		completed_at TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS plan_allocations (
		plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
		allocation_id INTEGER NOT NULL REFERENCES allocations(id) ON DELETE CASCADE,
		PRIMARY KEY (plan_id, allocation_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_plans_period ON plans(period)`,
	`CREATE INDEX IF NOT EXISTS idx_plans_status ON plans(status)`,
	`CREATE INDEX IF NOT EXISTS idx_plan_alloc_allocation ON plan_allocations(allocation_id)`,
	`CREATE TABLE IF NOT EXISTS stock_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		category_key TEXT NOT NULL,
		category_name TEXT NOT NULL,
		unit TEXT NOT NULL DEFAULT '件',
		qty REAL NOT NULL DEFAULT 1,
		amount_cents INTEGER NOT NULL DEFAULT 0,
		expiry_date TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'in_stock',
		entry_id INTEGER REFERENCES entries(id) ON DELETE SET NULL,
		auto_created INTEGER NOT NULL DEFAULT 0,
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS stock_movements (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id INTEGER NOT NULL REFERENCES stock_items(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		qty REAL NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_stock_entry ON stock_items(entry_id)`,
	`CREATE INDEX IF NOT EXISTS idx_stock_category ON stock_items(category_key)`,
	`CREATE INDEX IF NOT EXISTS idx_stock_expiry ON stock_items(expiry_date)`,
}

type seedCategory struct {
	name     string
	kind     string
	children []string
}

var seedCategories = []seedCategory{
	{"餐饮", "expense", []string{"外卖", "买菜生鲜", "外出就餐", "零食饮料"}},
	{"交通", "expense", []string{"公交地铁", "打车"}},
	{"汽车", "expense", []string{"加油", "停车费", "保养", "维修", "保险"}},
	{"居住", "expense", []string{"房租", "水电燃气", "物业费", "家居用品"}},
	{"日用百货", "expense", []string{"日用品", "衣物鞋包", "数码家电"}},
	{"医疗健康", "expense", []string{"药品", "就诊", "体检"}},
	{"宠物", "expense", []string{"猫粮猫罐头", "猫砂", "宠物医疗", "宠物用品"}},
	{"保险", "expense", []string{"人身险", "财产险"}},
	{"教育学习", "expense", []string{"课程", "书籍"}},
	{"娱乐休闲", "expense", []string{"影音", "旅行", "运动"}},
	{"人情往来", "expense", []string{"红包礼金", "馈赠"}},
	{"囤货采购", "expense", []string{"瓜果蔬菜", "调料", "粮油", "肉蛋奶", "药品储备"}},
	{"其他支出", "expense", nil},
	{"工资收入", "income", nil},
	{"奖金补贴", "income", nil},
	{"兼职副业", "income", nil},
	{"理财收益", "income", nil},
	{"报销退款", "income", nil},
	{"其他收入", "income", nil},
}

var seedTags = []string{"猫咪", "房租", "保险", "旅行", "囤货", "大件", "待报销", "周期固定"}

func seed(d *sql.DB) error {
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM members`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil // 已初始化，不覆盖用户数据
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, name := range []string{"成员 A", "成员 B"} {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO members(name, created_at) VALUES(?, ?)`, name, nowText()); err != nil {
			return err
		}
	}
	for i, c := range seedCategories {
		res, err := tx.Exec(`INSERT INTO categories(parent_id, name, kind, sort_order) VALUES(NULL, ?, ?, ?)`, c.name, c.kind, i)
		if err != nil {
			return err
		}
		pid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for j, child := range c.children {
			if _, err := tx.Exec(`INSERT INTO categories(parent_id, name, kind, sort_order) VALUES(?, ?, ?, ?)`, pid, child, c.kind, j); err != nil {
				return err
			}
		}
	}
	for _, t := range seedTags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags(name) VALUES(?)`, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

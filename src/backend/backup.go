package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 数据库结构版本。今后任何 ALTER TABLE / 改字段 / 数据迁移都必须：
//  1. 把 schemaVersion 加 1；
//  2. 在 migrations 里登记该版本的迁移语句；
//  3. 保留下方「变更前强制备份」逻辑 —— 版本落后时先备份再改，备份失败即中止启动。
//
// 备份使用 VACUUM INTO，生成包含 WAL 已提交数据的单文件一致性快照。
const schemaVersion = 4
const maxStartupBackups = 20

// migrations[版本号] = 从「版本号-1」升级到「版本号」要执行的语句。
// v1 只是给既有库打版本号标记（表结构由 schemaStatements 幂等维护），无破坏性语句。
// v2 新增「汽车」领域并把自家的用车支出归拢进去：
//   - 交通 只保留公共交通/打车；加油、停车费 迁移到 汽车 下（改 parent_id，保留 id，历史流水不受影响）
//   - 保险 只保留人身险/财产险；车险 迁移到 汽车 下并更名为「保险」
//   - 新增 保养、维修
//
// 所有语句都带存在性判断，重复执行不会产生重复分类。
var migrations = map[int][]string{
	1: {},
	2: {
		// ① 先给 汽车 腾位置：仅当库里还没有 汽车 时，把 交通 之后的一级分类整体后移一位
		`UPDATE categories SET sort_order = sort_order + 1
		 WHERE parent_id IS NULL
		   AND sort_order >= (SELECT sort_order + 1 FROM categories
		                      WHERE parent_id IS NULL AND name = '交通' ORDER BY id LIMIT 1)
		   AND NOT EXISTS (SELECT 1 FROM categories WHERE parent_id IS NULL AND name = '汽车')`,
		// ② 顶级「汽车」，占 交通 之后的位置
		`INSERT INTO categories(parent_id, name, kind, sort_order)
		 SELECT NULL, '汽车', 'expense',
		        COALESCE((SELECT sort_order + 1 FROM categories WHERE parent_id IS NULL AND name = '交通'), 1)
		 WHERE NOT EXISTS (SELECT 1 FROM categories WHERE parent_id IS NULL AND name = '汽车')`,
		// ③ 补 保养 / 维修
		`INSERT INTO categories(parent_id, name, kind, sort_order)
		 SELECT p.id, '保养', 'expense', 2 FROM categories p
		 WHERE p.parent_id IS NULL AND p.name = '汽车'
		   AND NOT EXISTS (SELECT 1 FROM categories c WHERE c.parent_id = p.id AND c.name = '保养')`,
		`INSERT INTO categories(parent_id, name, kind, sort_order)
		 SELECT p.id, '维修', 'expense', 3 FROM categories p
		 WHERE p.parent_id IS NULL AND p.name = '汽车'
		   AND NOT EXISTS (SELECT 1 FROM categories c WHERE c.parent_id = p.id AND c.name = '维修')`,
		// ④ 加油 / 停车费 从 交通 迁过来（保留原 id）
		`UPDATE categories
		 SET parent_id = (SELECT id FROM categories WHERE parent_id IS NULL AND name = '汽车' ORDER BY id LIMIT 1),
		     sort_order = 0
		 WHERE name = '加油'
		   AND parent_id = (SELECT id FROM categories WHERE parent_id IS NULL AND name = '交通' ORDER BY id LIMIT 1)`,
		`UPDATE categories
		 SET parent_id = (SELECT id FROM categories WHERE parent_id IS NULL AND name = '汽车' ORDER BY id LIMIT 1),
		     sort_order = 1
		 WHERE name = '停车费'
		   AND parent_id = (SELECT id FROM categories WHERE parent_id IS NULL AND name = '交通' ORDER BY id LIMIT 1)`,
		// ⑤ 车险 -> 汽车/保险（改 parent_id 与 name，保留 id）
		`UPDATE categories
		 SET name = '保险',
		     parent_id = (SELECT id FROM categories WHERE parent_id IS NULL AND name = '汽车' ORDER BY id LIMIT 1),
		     sort_order = 4
		 WHERE name = '车险'
		   AND parent_id = (SELECT id FROM categories WHERE parent_id IS NULL AND name = '保险' ORDER BY id LIMIT 1)
		   AND NOT EXISTS (SELECT 1 FROM categories c JOIN categories p2 ON p2.id = c.parent_id
		                   WHERE p2.parent_id IS NULL AND p2.name = '汽车' AND c.name = '保险')`,
		// ⑥ 兜底：汽车 下仍无 保险 时新建
		`INSERT INTO categories(parent_id, name, kind, sort_order)
		 SELECT p.id, '保险', 'expense', 4 FROM categories p
		 WHERE p.parent_id IS NULL AND p.name = '汽车'
		   AND NOT EXISTS (SELECT 1 FROM categories c WHERE c.parent_id = p.id AND c.name = '保险')`,
	},
	3: {
		// v3 只修数据、不改结构：纠正库存品类「key 与 name 自相矛盾」的历史记录。
		// 早期手工入库无论有没有选品类，都只拿物品名去匹配关键词来定显示名，于是
		// 「别嘌醇缓释胶囊」选了药品却因为名字里没有「药」字被写成 name='其他'。
		// 这里按 key 反查回正确名称，key 本身不动。
		`UPDATE stock_items SET category_name = '药品'
		 WHERE category_key = 'medicine' AND COALESCE(TRIM(category_name), '') IN ('', '其他')`,
		`UPDATE stock_items SET category_name = '猫罐头'
		 WHERE category_key = 'cat_food' AND COALESCE(TRIM(category_name), '') IN ('', '其他')`,
		`UPDATE stock_items SET category_name = '瓜果蔬菜'
		 WHERE category_key = 'fruit_veg' AND COALESCE(TRIM(category_name), '') IN ('', '其他')`,
		`UPDATE stock_items SET category_name = '肉蛋奶'
		 WHERE category_key = 'meat_egg_milk' AND COALESCE(TRIM(category_name), '') IN ('', '其他')`,
		`UPDATE stock_items SET category_name = '调料'
		 WHERE category_key = 'seasoning' AND COALESCE(TRIM(category_name), '') IN ('', '其他')`,
		`UPDATE stock_items SET category_name = '粮油'
		 WHERE category_key = 'grain_oil' AND COALESCE(TRIM(category_name), '') IN ('', '其他')`,
		// 另一类：当时名称没命中关键词、直接落成「其他」的药品，按剂型词补回。
		// 排除咖啡胶囊、薯片、麦片等含剂型词但非药品的商品。
		`UPDATE stock_items SET category_key = 'medicine', category_name = '药品'
		 WHERE category_key IN ('', 'other')
		   AND COALESCE(TRIM(category_name), '') IN ('', '其他')
		   AND (name LIKE '%胶囊%' OR name LIKE '%缓释%' OR name LIKE '%肠溶%'
		        OR name LIKE '%片剂%' OR name LIKE '%分散片%' OR name LIKE '%咀嚼片%'
		        OR name LIKE '%泡腾片%' OR name LIKE '%含片%' OR name LIKE '%颗粒剂%'
		        OR name LIKE '%冲剂%' OR name LIKE '%口服液%' OR name LIKE '%滴眼液%'
		        OR name LIKE '%软膏%' OR name LIKE '%乳膏%' OR name LIKE '%栓剂%'
		        OR name LIKE '%注射液%' OR name LIKE '%糖浆%')
		   AND name NOT LIKE '%咖啡%' AND name NOT LIKE '%薯片%' AND name NOT LIKE '%麦片%'
		   AND name NOT LIKE '%鱼丸%' AND name NOT LIKE '%肉丸%' AND name NOT LIKE '%奶片%'`,
	},
	4: {
		// v4 新增「计划单」：plans / plan_allocations 两张表由 db.go 的
		// schemaStatements 幂等创建（CREATE TABLE IF NOT EXISTS），此处无历史数据要改，
		// 仅登记版本以便 preSchemaGuard 在变更前强制备份。
		// 旧库没有任何计划单，不需要回填；现有预算、流水一行都不动。
	},
}

type backupMeta struct {
	CreatedAt      string         `json:"created_at"`
	Label          string         `json:"label"`
	Source         string         `json:"source"`
	Backup         string         `json:"backup"`
	SizeBytes      int64          `json:"size_bytes"`
	SHA256_16      string         `json:"sha256_16"`
	IntegrityCheck string         `json:"integrity_check"`
	UserVersion    int            `json:"user_version"`
	TableRows      map[string]int `json:"table_rows"`
}

func backupDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "backups")
}

func tableRowCounts(d *sql.DB) (map[string]int, error) {
	rs, err := d.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rs.Next() {
		var n string
		if err := rs.Scan(&n); err != nil {
			rs.Close()
			return nil, err
		}
		names = append(names, n)
	}
	if err := rs.Close(); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(names))
	for _, n := range names {
		var c int
		if err := d.QueryRow(`SELECT COUNT(*) FROM "` + n + `"`).Scan(&c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, nil
}

func fileSHA16(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// backupDB 生成一份可独立恢复的备份（.db + .json 元数据）。
// label 建议: before-migration-vN / startup / before-reset / manual
func backupDB(d *sql.DB, dbPath, label string) (string, error) {
	dir := backupDir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	safe := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r > 127 {
			return r
		}
		return '-'
	}, label)
	safe = strings.Trim(safe, "-")
	if safe == "" {
		safe = "manual"
	}
	base, path := "", ""
	for i := 0; ; i++ {
		if i == 0 {
			base = fmt.Sprintf("%s_%s", stamp, safe)
		} else {
			base = fmt.Sprintf("%s_%s-%d", stamp, safe, i)
		}
		path = filepath.Join(dir, base+".db")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
	}
	// VACUUM INTO 在 WAL 模式下同样输出完整、已提交的单文件数据库。
	vacuum := fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(filepath.ToSlash(path), "'", "''"))
	if _, err := d.Exec(vacuum); err != nil {
		return "", fmt.Errorf("VACUUM INTO 失败: %w", err)
	}

	rows, err := tableRowCounts(d)
	if err != nil {
		return path, fmt.Errorf("备份已生成，但统计行数失败: %w", err)
	}
	hash, err := fileSHA16(path)
	if err != nil {
		return path, fmt.Errorf("备份已生成，但计算哈希失败: %w", err)
	}
	var integrity string
	_ = d.QueryRow(`PRAGMA integrity_check`).Scan(&integrity)
	st, _ := os.Stat(path)
	meta := backupMeta{
		CreatedAt:      time.Now().Format(time.RFC3339),
		Label:          label,
		Source:         "data/finance.db",
		Backup:         filepath.ToSlash(filepath.Join("data", "backups", base+".db")),
		SizeBytes:      st.Size(),
		SHA256_16:      hash,
		IntegrityCheck: integrity,
		UserVersion:    currentUserVersion(d),
		TableRows:      rows,
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, base+".json"), b, 0o644); err != nil {
		return path, fmt.Errorf("备份已生成，但写元数据失败: %w", err)
	}
	return path, nil
}

func currentUserVersion(d *sql.DB) int {
	var v int
	_ = d.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v
}

// preSchemaGuard 在任何 DDL 之前调用：版本落后时先备份，备份失败则拒绝启动。
func preSchemaGuard(d *sql.DB, dbPath string) error {
	v := currentUserVersion(d)
	if v >= schemaVersion {
		return nil
	}
	p, err := backupDB(d, dbPath, fmt.Sprintf("before-migration-v%d", schemaVersion))
	if err != nil {
		return fmt.Errorf("数据库结构变更前备份失败，已中止启动（未改动任何数据）: %w", err)
	}
	log.Printf("结构变更前已备份: %s", filepath.Base(p))
	return nil
}

// applyMigrations 执行 DDL 之后，跑各版本迁移语句并写入新版本号。
func applyMigrations(d *sql.DB) error {
	v := currentUserVersion(d)
	for ver := v + 1; ver <= schemaVersion; ver++ {
		for _, stmt := range migrations[ver] {
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("迁移到 v%d 失败: %w", ver, err)
			}
		}
	}
	if v < schemaVersion {
		if _, err := d.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
			return err
		}
		log.Printf("数据库结构版本: v%d -> v%d", v, schemaVersion)
	}
	return nil
}

// startupSnapshot 每次启动留一份快照（30 分钟内已有则跳过），滚动保留最近 20 份。
// 这是「真实数据可恢复」的兜底层：即使某次误操作没手动备份，也能回到最近一次启动的状态。
func startupSnapshot(d *sql.DB, dbPath string) {
	dir := backupDir(dbPath)
	entries, _ := os.ReadDir(dir)
	var newest time.Time
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".db") || !strings.Contains(name, "_startup") || len(name) < 15 {
			continue
		}
		if t, err := time.ParseInLocation("20060102-150405", name[:15], time.Local); err == nil && t.After(newest) {
			newest = t
		}
	}
	if time.Since(newest) < 30*time.Minute {
		return
	}
	p, err := backupDB(d, dbPath, "startup")
	if err != nil {
		log.Printf("警告：启动快照失败（不影响服务）: %v", err)
		return
	}
	log.Printf("启动快照: %s", filepath.Base(p))

	pruneByLabel(dir, "startup", maxStartupBackups)
}

func pruneByLabel(dir, label string, keep int) {
	files, _ := filepath.Glob(filepath.Join(dir, "*_"+label+"*.db"))
	sort.Strings(files)
	if len(files) > keep {
		for _, f := range files[:len(files)-keep] {
			_ = os.Remove(f)
			_ = os.Remove(strings.TrimSuffix(f, ".db") + ".json")
		}
	}
}

// dataFingerprint 用一组轻量聚合值判断业务数据是否变化。
// 覆盖新增、删除、改金额、改数量、截图与基础配置增删；取值失败时返回空串，
// 会保守地触发一次备份，宁可多备份也不漏。
func dataFingerprint(d *sql.DB) string {
	queries := []string{
		`SELECT COUNT(*), COALESCE(SUM(amount_cents),0), COALESCE(MAX(updated_at),'') FROM entries`,
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(data)),0) FROM screenshots`,
		`SELECT COUNT(*), COALESCE(SUM(qty),0), COALESCE(MAX(updated_at),'') FROM stock_items`,
		`SELECT COUNT(*) FROM stock_movements`,
		`SELECT COUNT(*), COALESCE(SUM(budget_cents),0) FROM allocations`,
		`SELECT COUNT(*) FROM entry_tags`,
		`SELECT COUNT(*) FROM categories`,
		`SELECT COUNT(*) FROM members`,
		`SELECT COUNT(*) FROM tags`,
	}
	var sb strings.Builder
	for _, q := range queries {
		rs, err := d.Query(q)
		if err != nil {
			return ""
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if rs.Scan(ptrs...) == nil {
				for _, v := range vals {
					fmt.Fprintf(&sb, "%v|", v)
				}
			}
		}
		rs.Close()
		sb.WriteString(";")
	}
	return sb.String()
}

// startSnapshotLoop 是试用期间真实数据的兜底层：服务长期不重启时，
// 只要数据发生变化就自动留一份快照；每天的第一份标为 daily-YYYYMMDD 永久保留，
// 其余标为 auto 滚动保留最近 48 份（默认 30 分钟一轮即覆盖约 24 小时）。
func startSnapshotLoop(d *sql.DB, dbPath string, interval time.Duration) func() {
	if interval <= 0 {
		log.Printf("运行时自动快照：已关闭")
		return func() {}
	}
	log.Printf("运行时自动快照：每 %s 检查一次，数据有变化才备份", interval)
	stop := make(chan struct{})
	go func() {
		last := dataFingerprint(d)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fp := dataFingerprint(d)
				if fp == last {
					continue // 数据没动，不产生重复快照
				}
				last = fp
				today := time.Now().Format("20060102")
				label := "auto"
				if hits, _ := filepath.Glob(filepath.Join(backupDir(dbPath), "*_daily-"+today+"*.db")); len(hits) == 0 {
					label = "daily-" + today
				}
				p, err := backupDB(d, dbPath, label)
				if err != nil {
					log.Printf("警告：自动快照失败（不影响服务）: %v", err)
					continue
				}
				log.Printf("检测到数据变化，已自动快照: %s", filepath.Base(p))
				pruneByLabel(backupDir(dbPath), "auto", 48)
			}
		}
	}()
	return func() { close(stop) }
}

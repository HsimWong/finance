package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---------- 库存品类（可用户维护）----------
//
// 早期品类是 parse.go 里写死的 6 条 stockRules，用户没法加新品类。
// 现在品类落库到 stock_categories，内置 6 类作为种子写入，之后完全由用户维护。
// 关键约定：
//   - key 是稳定标识，stock_items.category_key 引用它；改名不动 key，历史数据不受影响。
//   - stockRules（parse.go）保留为「表为空 / db 不可用」时的兜底，保证单例测试与冷启动安全。
//   - 自定义品类（is_builtin=0）在 OCR 匹配时排在内置之前，让用户关键词优先生效。

type StockCategory struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	Keywords  string `json:"keywords"`
	ShelfDays int    `json:"shelf_days"`
	SortOrder int    `json:"sort_order"`
	IsBuiltin bool   `json:"is_builtin"`
	ItemCount int    `json:"item_count"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// normalizeKeywords 统一中文/英文逗号分隔、去空白、去重，落库为英文逗号串。
func normalizeKeywords(s string) string {
	s = strings.ReplaceAll(s, "，", ",")
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, k := range strings.Split(s, ",") {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return strings.Join(out, ",")
}

// loadStockRules 运行时从库里读品类规则；库不可用或为空时回退到内置兜底。
// 排序：自定义品类优先（is_builtin 0 在前），其次按用户设定的 sort_order。
func loadStockRules() []stockRule {
	if db == nil {
		return stockRules
	}
	rows, err := db.Query(`SELECT key, name, keywords, shelf_days FROM stock_categories
		ORDER BY is_builtin ASC, sort_order ASC, id ASC`)
	if err != nil {
		return stockRules
	}
	defer rows.Close()
	out := make([]stockRule, 0)
	for rows.Next() {
		var r stockRule
		var keywords string
		if err := rows.Scan(&r.Key, &r.Name, &keywords, &r.ShelfDays); err != nil {
			return stockRules
		}
		for _, kw := range strings.Split(keywords, ",") {
			if kw = strings.TrimSpace(kw); kw != "" {
				r.Keywords = append(r.Keywords, kw)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil || len(out) == 0 {
		return stockRules
	}
	return out
}

// seedStockCategories 首次启动时把内置 6 类写入品类表。
// 只要表里已有任意行就完全不动——用户删掉某个内置品类后不会被下次启动加回来。
func seedStockCategories(d *sql.DB) error {
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM stock_categories`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowText()
	for i, r := range stockRules {
		if _, err := tx.Exec(`INSERT INTO stock_categories
			(key, name, keywords, shelf_days, sort_order, is_builtin, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, 1, ?, ?)`,
			r.Key, r.Name, normalizeKeywords(strings.Join(r.Keywords, ",")), r.ShelfDays, i, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func queryStockCategories() ([]StockCategory, error) {
	rows, err := db.Query(`SELECT c.id, c.key, c.name, c.keywords, c.shelf_days, c.sort_order,
		c.is_builtin, c.created_at, c.updated_at,
		(SELECT COUNT(*) FROM stock_items s WHERE s.category_key = c.key)
		FROM stock_categories c ORDER BY c.is_builtin ASC, c.sort_order ASC, c.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StockCategory, 0)
	for rows.Next() {
		var c StockCategory
		var builtin int
		if err := rows.Scan(&c.ID, &c.Key, &c.Name, &c.Keywords, &c.ShelfDays, &c.SortOrder,
			&builtin, &c.CreatedAt, &c.UpdatedAt, &c.ItemCount); err != nil {
			return nil, err
		}
		c.IsBuiltin = builtin == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func handleListStockCategories(w http.ResponseWriter, r *http.Request) {
	list, err := queryStockCategories()
	if err != nil {
		writeErr(w, 500, "读取品类失败: %v", err)
		return
	}
	writeOK(w, list)
}

func handleCreateStockCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		Keywords  string `json:"keywords"`
		ShelfDays int    `json:"shelf_days"`
		SortOrder int    `json:"sort_order"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeErr(w, 400, "请提供品类名称")
		return
	}
	if in.ShelfDays <= 0 || in.ShelfDays > 3650 {
		in.ShelfDays = 365
	}
	// key 用纳秒时间戳生成，保留 custom_ 前缀以便一眼区分内置/自定义。
	key := "custom_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	now := nowText()
	res, err := db.Exec(`INSERT INTO stock_categories
		(key, name, keywords, shelf_days, sort_order, is_builtin, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, 0, ?, ?)`,
		key, in.Name, normalizeKeywords(in.Keywords), in.ShelfDays, in.SortOrder, now, now)
	if err != nil {
		writeErr(w, 400, "新增失败: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	list, err := queryStockCategories()
	if err != nil {
		writeOK(w, map[string]any{"id": id, "key": key})
		return
	}
	for _, c := range list {
		if c.ID == id {
			writeOK(w, c)
			return
		}
	}
	writeOK(w, map[string]any{"id": id, "key": key})
}

func handleUpdateStockCategory(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in struct {
		Name      *string `json:"name"`
		Keywords  *string `json:"keywords"`
		ShelfDays *int    `json:"shelf_days"`
		SortOrder *int    `json:"sort_order"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	var oldKey, oldName string
	if err := db.QueryRow(`SELECT key, name FROM stock_categories WHERE id = ?`, id).Scan(&oldKey, &oldName); err != nil {
		writeErr(w, 404, "品类不存在")
		return
	}
	name := oldName
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" {
			writeErr(w, 400, "品类名称不能为空")
			return
		}
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "开启事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	var keywords any
	if in.Keywords != nil {
		keywords = normalizeKeywords(*in.Keywords)
	}
	var shelf any
	if in.ShelfDays != nil {
		if *in.ShelfDays <= 0 || *in.ShelfDays > 3650 {
			writeErr(w, 400, "保质期天数需在 1~3650 之间")
			return
		}
		shelf = *in.ShelfDays
	}
	var sort any
	if in.SortOrder != nil {
		sort = *in.SortOrder
	}
	if _, err := tx.Exec(`UPDATE stock_categories SET name=?,
		keywords=COALESCE(?, keywords), shelf_days=COALESCE(?, shelf_days),
		sort_order=COALESCE(?, sort_order), updated_at=? WHERE id=?`,
		name, keywords, shelf, sort, nowText(), id); err != nil {
		writeErr(w, 500, "更新失败: %v", err)
		return
	}
	// category_name 是冗余显示字段，改名后同步历史库存项，避免 key/name 不一致。
	if name != oldName {
		if _, err := tx.Exec(`UPDATE stock_items SET category_name=? WHERE category_key=?`, name, oldKey); err != nil {
			writeErr(w, 500, "同步库存品类名称失败: %v", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	list, err := queryStockCategories()
	if err != nil {
		writeOK(w, map[string]any{"id": id})
		return
	}
	for _, c := range list {
		if c.ID == id {
			writeOK(w, c)
			return
		}
	}
	writeOK(w, map[string]any{"id": id})
}

func handleDeleteStockCategory(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var key, name string
	if err := db.QueryRow(`SELECT key, name FROM stock_categories WHERE id = ?`, id).Scan(&key, &name); err != nil {
		writeErr(w, 404, "品类不存在")
		return
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM stock_items WHERE category_key = ?`, key).Scan(&n)
	if n > 0 {
		writeErr(w, 400, "该品类下还有 %d 项库存，请先把它们改到其他品类", n)
		return
	}
	if _, err := db.Exec(`DELETE FROM stock_categories WHERE id = ?`, id); err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id, "name": name})
}

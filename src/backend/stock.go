package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---------- 数据结构 ----------

type stockItemInput struct {
	Name         string  `json:"name"`
	Qty          float64 `json:"qty"`
	Unit         string  `json:"unit"`
	Amount       Money   `json:"amount"`
	CategoryKey  string  `json:"category_key"`
	CategoryName string  `json:"category_name"`
	ExpiryDate   string  `json:"expiry_date"`
	Note         string  `json:"note"`
}

type StockItem struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	CategoryKey  string  `json:"category_key"`
	CategoryName string  `json:"category_name"`
	Unit         string  `json:"unit"`
	Qty          float64 `json:"qty"`
	AmountCents  int64   `json:"amount_cents"`
	Amount       string  `json:"amount"`
	ExpiryDate   string  `json:"expiry_date"`
	DaysLeft     *int    `json:"days_left"`
	ExpiryStatus string  `json:"expiry_status"` // "" / soon / expired
	Status       string  `json:"status"`
	EntryID      *int64  `json:"entry_id"`
	AutoCreated  bool    `json:"auto_created"`
	Note         string  `json:"note"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

type StockCategorySummary struct {
	Key          string  `json:"key"`
	Name         string  `json:"name"`
	ItemCount    int     `json:"item_count"`
	TotalQty     float64 `json:"total_qty"`
	AmountCents  int64   `json:"amount_cents"`
	Amount       string  `json:"amount"`
	Expired      int     `json:"expired"`
	ExpiringSoon int     `json:"expiring_soon"`
}

type StockMovement struct {
	ID        int64   `json:"id"`
	ItemID    int64   `json:"item_id"`
	ItemName  string  `json:"item_name"`
	Kind      string  `json:"kind"`
	Qty       float64 `json:"qty"`
	Note      string  `json:"note"`
	CreatedAt string  `json:"created_at"`
}

const stockSelect = `SELECT s.id, s.name, s.category_key, s.category_name, s.unit, s.qty,
	s.amount_cents, s.expiry_date, s.status, s.entry_id, s.auto_created,
	s.note, s.created_at, s.updated_at FROM stock_items s`

func todayStr() string { return time.Now().Format("2006-01-02") }

func daysLeft(expiry string) *int {
	if expiry == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", expiry)
	if err != nil {
		return nil
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	d := int(t.Sub(today).Hours() / 24)
	return &d
}

func fillExpiry(s *StockItem) {
	s.Amount = FormatCents(s.AmountCents)
	s.DaysLeft = daysLeft(s.ExpiryDate)
	s.ExpiryStatus = ""
	if s.Status != "in_stock" || s.DaysLeft == nil {
		return
	}
	if *s.DaysLeft < 0 {
		s.ExpiryStatus = "expired"
	} else if *s.DaysLeft <= 3 {
		s.ExpiryStatus = "soon"
	}
}

func scanStockItems(rows *sql.Rows) ([]StockItem, error) {
	defer rows.Close()
	out := make([]StockItem, 0)
	for rows.Next() {
		var s StockItem
		var eid sql.NullInt64
		var auto int
		if err := rows.Scan(&s.ID, &s.Name, &s.CategoryKey, &s.CategoryName, &s.Unit, &s.Qty,
			&s.AmountCents, &s.ExpiryDate, &s.Status, &eid, &auto, &s.Note,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.EntryID = nullIntPtr(eid)
		s.AutoCreated = auto == 1
		fillExpiry(&s)
		out = append(out, s)
	}
	return out, rows.Err()
}

func queryStockItems(v map[string]string) ([]StockItem, error) {
	conds := []string{"1=1"}
	args := []any{}
	if k := v["category_key"]; k != "" {
		conds = append(conds, "s.category_key = ?")
		args = append(args, k)
	}
	if st := v["status"]; st != "" {
		conds = append(conds, "s.status = ?")
		args = append(args, st)
	}
	if kw := strings.TrimSpace(v["q"]); kw != "" {
		conds = append(conds, "s.name LIKE ?")
		args = append(args, "%"+kw+"%")
	}
	if v["expiring"] == "1" {
		conds = append(conds, "s.status='in_stock' AND s.expiry_date <> '' AND s.expiry_date <= ?")
		args = append(args, time.Now().AddDate(0, 0, 3).Format("2006-01-02"))
	}
	rows, err := db.Query(stockSelect+` WHERE `+strings.Join(conds, " AND ")+`
		ORDER BY CASE WHEN s.expiry_date='' THEN 1 ELSE 0 END, s.expiry_date, s.id DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	return scanStockItems(rows)
}

func findStockRuleByKey(key string) (stockRule, bool) {
	for _, r := range stockRules {
		if r.Key == key {
			return r, true
		}
	}
	return stockRule{}, false
}

// resolveStockCategory 决定一个库存项最终落哪个品类。
//
// 关键约定：category_key 是调用方（前端下拉框、OCR 草稿）显式选择的品类，
// 一旦给了就以它为准、显示名从规则表反查；只有「未指定」时才按物品名去猜。
// 早先的实现无论 key 有没有给，都只按物品名匹配关键词来定名称，于是
// 「别嘌醇缓释胶囊」选了药品却因为名字里没有「药」字被写成「其他」——
// 库里 key=medicine、name=其他 这种自相矛盾的数据就是这么产生的。
func resolveStockCategory(key, catName, itemName string) (string, string) {
	key = strings.TrimSpace(key)
	catName = strings.TrimSpace(catName)
	if key != "" && key != "other" {
		if r, ok := findStockRuleByKey(key); ok {
			return r.Key, r.Name // 显式选择，以 key 为准
		}
	}
	if key != "other" {
		if r, ok := matchStockRule(itemName); ok {
			return r.Key, r.Name // 未指定品类，按名称推断
		}
	}
	if catName != "" && key != "" {
		return key, catName // key 不在规则表里但调用方给了名称，原样保留
	}
	return "other", "其他"
}

// saveAutoStockItems 在同一个事务里把识别出的家庭用品写入库存。
// 编辑流水时只重建「自动生成」的条目，用户手工维护的库存不会被覆盖。
func saveAutoStockItems(tx *sql.Tx, entryID int64, items []stockItemInput) error {
	if _, err := tx.Exec(`DELETE FROM stock_items WHERE entry_id = ? AND auto_created = 1`, entryID); err != nil {
		return err
	}
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		qty := it.Qty
		if qty <= 0 {
			qty = 1
		}
		unit := strings.TrimSpace(it.Unit)
		if unit == "" {
			unit = "件"
		}
		catName := strings.TrimSpace(it.CategoryName)
		if catName == "" {
			if r, ok := findStockRuleByKey(it.CategoryKey); ok {
				catName = r.Name
			} else {
				catName = "其他"
			}
		}
		key := it.CategoryKey
		if key == "" {
			key = "other"
		}
		expiry := strings.TrimSpace(it.ExpiryDate)
		if expiry == "" {
			if r, ok := findStockRuleByKey(key); ok {
				expiry = time.Now().AddDate(0, 0, r.ShelfDays).Format("2006-01-02")
			}
		}
		res, err := tx.Exec(`INSERT INTO stock_items
			(name, category_key, category_name, unit, qty, amount_cents, expiry_date,
			 status, entry_id, auto_created, note, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, 'in_stock', ?, 1, ?, ?, ?)`,
			name, key, catName, unit, qty, int64(it.Amount), expiry,
			entryID, it.Note, nowText(), nowText())
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO stock_movements(item_id, kind, qty, note, created_at)
			VALUES(?, 'in', ?, '账单识别自动入库', ?)`, id, qty, nowText()); err != nil {
			return err
		}
	}
	return nil
}

// ---------- HTTP ----------

func handleStockSummary(w http.ResponseWriter, r *http.Request) {
	today := todayStr()
	soon := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	rows, err := db.Query(`SELECT category_key, category_name, COUNT(*),
		COALESCE(SUM(qty), 0), COALESCE(SUM(amount_cents), 0),
		SUM(CASE WHEN expiry_date <> '' AND expiry_date < ? THEN 1 ELSE 0 END),
		SUM(CASE WHEN expiry_date <> '' AND expiry_date >= ? AND expiry_date <= ? THEN 1 ELSE 0 END)
		FROM stock_items WHERE status = 'in_stock'
		GROUP BY category_key, category_name ORDER BY category_name`, today, today, soon)
	if err != nil {
		writeErr(w, 500, "汇总失败: %v", err)
		return
	}
	defer rows.Close()
	cats := make([]StockCategorySummary, 0)
	totalItems, totalCents, expired, expiring := 0, int64(0), 0, 0
	for rows.Next() {
		var c StockCategorySummary
		if err := rows.Scan(&c.Key, &c.Name, &c.ItemCount, &c.TotalQty, &c.AmountCents, &c.Expired, &c.ExpiringSoon); err != nil {
			writeErr(w, 500, "扫描失败: %v", err)
			return
		}
		c.Amount = FormatCents(c.AmountCents)
		totalItems += c.ItemCount
		totalCents += c.AmountCents
		expired += c.Expired
		expiring += c.ExpiringSoon
		cats = append(cats, c)
	}
	alerts, err := queryStockItems(map[string]string{"expiring": "1"})
	if err != nil {
		alerts = []StockItem{}
	}
	writeOK(w, map[string]any{
		"categories":         cats,
		"total_items":        totalItems,
		"total_amount_cents": totalCents,
		"total_amount":       FormatCents(totalCents),
		"expired":            expired,
		"expiring_soon":      expiring,
		"alerts":             alerts,
	})
}

func handleListStockItems(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{}
	for _, k := range []string{"category_key", "status", "q", "expiring"} {
		v[k] = r.URL.Query().Get(k)
	}
	items, err := queryStockItems(v)
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	writeOK(w, items)
}

func handleCreateStockItem(w http.ResponseWriter, r *http.Request) {
	var in stockItemInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeErr(w, 400, "请填写物品名称")
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	// 复用自动入库的写入逻辑，但标记为手工录入、不关联流水
	if err := saveManualStockItem(tx, in); err != nil {
		writeErr(w, 400, "新增失败: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"name": name})
}

func saveManualStockItem(tx *sql.Tx, in stockItemInput) error {
	qty := in.Qty
	if qty <= 0 {
		qty = 1
	}
	unit := strings.TrimSpace(in.Unit)
	if unit == "" {
		unit = "件"
	}
	key, catName := resolveStockCategory(in.CategoryKey, in.CategoryName, in.Name)
	expiry := strings.TrimSpace(in.ExpiryDate)
	if expiry == "" {
		if r, ok := findStockRuleByKey(key); ok {
			expiry = time.Now().AddDate(0, 0, r.ShelfDays).Format("2006-01-02")
		}
	}
	res, err := tx.Exec(`INSERT INTO stock_items
		(name, category_key, category_name, unit, qty, amount_cents, expiry_date,
		 status, entry_id, auto_created, note, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, 'in_stock', NULL, 0, ?, ?, ?)`,
		strings.TrimSpace(in.Name), key, catName, unit, qty, int64(in.Amount), expiry,
		in.Note, nowText(), nowText())
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	_, err = tx.Exec(`INSERT INTO stock_movements(item_id, kind, qty, note, created_at)
		VALUES(?, 'in', ?, '手工入库', ?)`, id, qty, nowText())
	return err
}

func handleUpdateStockItem(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in struct {
		Name        string   `json:"name"`
		Qty         *float64 `json:"qty"`
		Unit        string   `json:"unit"`
		Amount      Money    `json:"amount"`
		CategoryKey string   `json:"category_key"`
		ExpiryDate  string   `json:"expiry_date"`
		Note        string   `json:"note"`
		Status      string   `json:"status"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	var cur StockItem
	var eid sql.NullInt64
	var auto int
	if err := db.QueryRow(stockSelect+` WHERE s.id = ?`, id).Scan(&cur.ID, &cur.Name, &cur.CategoryKey,
		&cur.CategoryName, &cur.Unit, &cur.Qty, &cur.AmountCents, &cur.ExpiryDate, &cur.Status,
		&eid, &auto, &cur.Note, &cur.CreatedAt, &cur.UpdatedAt); err != nil {
		writeErr(w, 404, "库存项不存在")
		return
	}
	if in.Name != "" {
		cur.Name = in.Name
	}
	if in.Qty != nil {
		cur.Qty = *in.Qty
	}
	if in.Unit != "" {
		cur.Unit = in.Unit
	}
	if in.Amount > 0 {
		cur.AmountCents = int64(in.Amount)
	}
	cur.ExpiryDate = in.ExpiryDate
	cur.Note = in.Note
	if in.Status == "in_stock" || in.Status == "used_up" || in.Status == "wasted" {
		cur.Status = in.Status
	}
	// 允许改品类：早先编辑弹窗没有这个字段，品类选错后无法在界面上修正
	if in.CategoryKey != "" {
		cur.CategoryKey, cur.CategoryName = resolveStockCategory(in.CategoryKey, "", cur.Name)
	}
	if _, err := db.Exec(`UPDATE stock_items SET name=?, category_key=?, category_name=?, qty=?, unit=?,
		amount_cents=?, expiry_date=?, note=?, status=?, updated_at=? WHERE id=?`,
		cur.Name, cur.CategoryKey, cur.CategoryName, cur.Qty, cur.Unit, cur.AmountCents,
		cur.ExpiryDate, cur.Note, cur.Status, nowText(), id); err != nil {
		writeErr(w, 500, "更新失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleDeleteStockItem(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	res, err := db.Exec(`DELETE FROM stock_items WHERE id = ?`, id)
	if err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "库存项不存在")
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func applyMovement(w http.ResponseWriter, r *http.Request, kind, label string) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in struct {
		Qty  *float64 `json:"qty"`
		Note string   `json:"note"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	var qty float64
	var status string
	if err := tx.QueryRow(`SELECT qty, status FROM stock_items WHERE id = ?`, id).Scan(&qty, &status); err != nil {
		writeErr(w, 404, "库存项不存在")
		return
	}
	delta := qty
	if in.Qty != nil && *in.Qty > 0 {
		delta = *in.Qty
	}
	if delta > qty {
		delta = qty
	}
	newQty := qty - delta
	newStatus := status
	if newQty <= 0.0001 {
		newQty = 0
		newStatus = map[string]string{"consume": "used_up", "waste": "wasted"}[kind]
	}
	if _, err := tx.Exec(`UPDATE stock_items SET qty=?, status=?, updated_at=? WHERE id=?`, newQty, newStatus, nowText(), id); err != nil {
		writeErr(w, 500, "更新失败: %v", err)
		return
	}
	note := in.Note
	if note == "" {
		note = label
	}
	if _, err := tx.Exec(`INSERT INTO stock_movements(item_id, kind, qty, note, created_at)
		VALUES(?, ?, ?, ?, ?)`, id, kind, delta, note, nowText()); err != nil {
		writeErr(w, 500, "记录变动失败: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id, "qty": newQty, "status": newStatus})
}

func handleConsumeStock(w http.ResponseWriter, r *http.Request) {
	applyMovement(w, r, "consume", "每日消耗")
}

func handleWasteStock(w http.ResponseWriter, r *http.Request) {
	applyMovement(w, r, "waste", "损耗报废")
}

func handleStockMovements(w http.ResponseWriter, r *http.Request) {
	args := []any{}
	where := "1=1"
	if s := r.URL.Query().Get("item_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			where = "m.item_id = ?"
			args = append(args, id)
		}
	}
	rows, err := db.Query(`SELECT m.id, m.item_id, COALESCE(s.name, ''), m.kind, m.qty, m.note, m.created_at
		FROM stock_movements m LEFT JOIN stock_items s ON s.id = m.item_id
		WHERE `+where+` ORDER BY m.id DESC LIMIT 200`, args...)
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	defer rows.Close()
	out := make([]StockMovement, 0)
	for rows.Next() {
		var m StockMovement
		if err := rows.Scan(&m.ID, &m.ItemID, &m.ItemName, &m.Kind, &m.Qty, &m.Note, &m.CreatedAt); err != nil {
			writeErr(w, 500, "扫描失败: %v", err)
			return
		}
		out = append(out, m)
	}
	writeOK(w, out)
}

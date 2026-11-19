package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 显式 sync_stock 语义的回归测试：
// 不传/false = 完全不动库存；true + 空数组 = 清空本流水自动项；true + 清单 = 重建自动项。
// 用户手工维护的库存（auto_created=0）在任何路径下都不受影响。

func syncPayload(extra map[string]any) map[string]any {
	p := map[string]any{
		"type": "expense", "amount": "20.00",
		"occurred_at": "2026-11-19T10:00:00", "note": "库存同步测试",
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func syncPostEntry(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/entries", strings.NewReader(string(b)))
	rec := httptest.NewRecorder()
	handleCreateEntry(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("新增流水失败 %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Data.ID
}

func syncPutEntry(t *testing.T, id int64, payload map[string]any) {
	t.Helper()
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/entries/%d", id), strings.NewReader(string(b)))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	rec := httptest.NewRecorder()
	handleUpdateEntry(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新流水失败 %d: %s", rec.Code, rec.Body.String())
	}
}

func syncCount(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStockSyncRequiresExplicitFlag(t *testing.T) {
	dir := t.TempDir()
	var err error
	db, err = openDB(filepath.Join(dir, "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 1. 不传 sync_stock：即使误传了识别物品，也绝不写库存
	entryID := syncPostEntry(t, syncPayload(map[string]any{
		"stock_items": []map[string]any{{"name": "不该进库存", "qty": 1}},
	}))
	if n := syncCount(t, `SELECT COUNT(*) FROM stock_items WHERE entry_id=?`, entryID); n != 0 {
		t.Fatalf("sync_stock 缺省时不应动库存，实际 %d 项", n)
	}

	// 2. sync_stock=true + 两项清单：同事务重建自动项，并留下入库流水
	syncPutEntry(t, entryID, syncPayload(map[string]any{
		"sync_stock": true,
		"stock_items": []map[string]any{
			{"name": "牛奶", "qty": 1, "unit": "盒", "amount": "12.00"},
			{"name": "鸡蛋", "qty": 2, "unit": "个", "amount": "8.00"},
		},
	}))
	if n := syncCount(t, `SELECT COUNT(*) FROM stock_items WHERE entry_id=? AND auto_created=1`, entryID); n != 2 {
		t.Fatalf("显式同步后应有 2 项自动库存，实际 %d", n)
	}
	if n := syncCount(t, `SELECT COUNT(*) FROM stock_movements m JOIN stock_items s ON s.id=m.item_id WHERE s.entry_id=?`, entryID); n != 2 {
		t.Fatalf("自动入库应产生 2 条库存变动，实际 %d", n)
	}

	// 造一条用户手工维护的库存，验证后续重建/清空都碰不到它
	res, err := db.Exec(`INSERT INTO stock_items
		(name, category_key, category_name, unit, qty, entry_id, auto_created, created_at, updated_at)
		VALUES('手工牛奶','other','其他','盒',1,?,0,?,?)`, entryID, nowText(), nowText())
	if err != nil {
		t.Fatal(err)
	}
	manualID, _ := res.LastInsertId()

	// 3. sync_stock=true + 一项：自动项重建为 1，手工项保留
	syncPutEntry(t, entryID, syncPayload(map[string]any{
		"sync_stock":  true,
		"stock_items": []map[string]any{{"name": "苹果", "qty": 1, "amount": "5.00"}},
	}))
	if n := syncCount(t, `SELECT COUNT(*) FROM stock_items WHERE entry_id=? AND auto_created=1 AND name='苹果'`, entryID); n != 1 {
		t.Fatalf("重建后应只剩苹果 1 项，实际 %d", n)
	}
	assertManualStockAlive(t, manualID)

	// 4. sync_stock=true + 空数组：明确清空本流水自动项，手工项仍在
	syncPutEntry(t, entryID, syncPayload(map[string]any{
		"sync_stock": true,
		"stock_items": []map[string]any{},
	}))
	if n := syncCount(t, `SELECT COUNT(*) FROM stock_items WHERE entry_id=? AND auto_created=1`, entryID); n != 0 {
		t.Fatalf("显式清空后自动项应为 0，实际 %d", n)
	}
	assertManualStockAlive(t, manualID)

	// 5. sync_stock=false + 空数组：不碰库存（先造一项，确认它不会被清空）
	syncPutEntry(t, entryID, syncPayload(map[string]any{
		"sync_stock":  true,
		"stock_items": []map[string]any{{"name": "面包", "qty": 1, "amount": "6.00"}},
	}))
	syncPutEntry(t, entryID, syncPayload(map[string]any{
		"sync_stock":  false,
		"stock_items": []map[string]any{},
	}))
	if n := syncCount(t, `SELECT COUNT(*) FROM stock_items WHERE entry_id=? AND auto_created=1 AND name='面包'`, entryID); n != 1 {
		t.Fatalf("sync_stock=false 不应动库存，面包应仍在，实际 %d", n)
	}
	assertManualStockAlive(t, manualID)
}

func assertManualStockAlive(t *testing.T, id int64) {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT name FROM stock_items WHERE id=?`, id).Scan(&name); err != nil {
		t.Fatalf("用户手工库存被误删: %v", err)
	}
	if name != "手工牛奶" {
		t.Fatalf("用户手工库存被改写，name=%q", name)
	}
}

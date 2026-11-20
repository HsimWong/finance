package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// 消耗/损耗统计：只用 t.TempDir()，不碰 data/finance.db。
// 验证按 kind 汇总（笔数/数量/物品数）、品类与物品排行、月份筛选。
func TestStockConsumptionStat(t *testing.T) {
	d, err := openDB(filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := db
	db = d
	defer func() { db.Close(); db = old }()

	// 三件物品，两个品类
	for i, name := range []string{"牛奶", "鸡蛋", "猫粮"} {
		cat, key := "食品", "food"
		if i == 2 {
			cat, key = "宠物", "pet"
		}
		if _, err := db.Exec(`INSERT INTO stock_items(name, category_key, category_name, qty, created_at, updated_at)
			VALUES(?, ?, ?, 10, '2026-11-01 00:00:00', '2026-11-01 00:00:00')`, name, key, cat); err != nil {
			t.Fatal(err)
		}
	}
	// milk=1, egg=2, catfood=3
	moves := []struct {
		item  int64
		kind  string
		qty   float64
		when  string
	}{
		{1, "consume", 2, "2026-11-01 08:00:00"},
		{1, "consume", 3, "2026-11-15 08:00:00"},
		{2, "waste", 1, "2026-11-03 08:00:00"},
		{3, "consume", 4, "2026-10-20 08:00:00"}, // 上个月，应被 period 过滤
	}
	for _, m := range moves {
		if _, err := db.Exec(`INSERT INTO stock_movements(item_id, kind, qty, note, created_at)
			VALUES(?, ?, ?, '', ?)`, m.item, m.kind, m.qty, m.when); err != nil {
			t.Fatal(err)
		}
	}

	var out stockConsumeStat
	fetch := func(url string) stockConsumeStat {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		handleStockConsumption(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("统计失败 %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data stockConsumeStat `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data
	}

	// 全部月份
	out = fetch("/api/stock/consumption")
	if out.Consume.Count != 3 || out.Consume.Qty != 9 || out.Consume.Items != 2 {
		t.Errorf("consume 汇总不符: %+v", out.Consume)
	}
	if out.Waste.Count != 1 || out.Waste.Qty != 1 || out.Waste.Items != 1 {
		t.Errorf("waste 汇总不符: %+v", out.Waste)
	}
	if len(out.ByCategory) != 2 || len(out.ByItem) != 3 {
		t.Errorf("排行条数不符: cat=%d item=%d", len(out.ByCategory), len(out.ByItem))
	}

	// 仅 2026-11：排除上月猫粮 consume
	out = fetch("/api/stock/consumption?period=2026-11")
	if out.Consume.Count != 2 || out.Consume.Qty != 5 || out.Consume.Items != 1 {
		t.Errorf("11 月 consume 汇总不符: %+v", out.Consume)
	}
	if out.Waste.Count != 1 || out.Waste.Qty != 1 {
		t.Errorf("11 月 waste 汇总不符: %+v", out.Waste)
	}

	// 空月份
	out = fetch("/api/stock/consumption?period=2026-09")
	if out.Consume.Count != 0 || out.Waste.Count != 0 || len(out.ByItem) != 0 {
		t.Errorf("空月份应全 0: %+v", out)
	}
}

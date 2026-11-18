package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 品类管理测试只用临时库，不碰 data/finance.db。
// 注意 loadStockRules 读全局 db，测试里临时替换并在结束还原。
func withTempCatsDB(t *testing.T) *sql.DB {
	t.Helper()
	old := db
	d, err := openDB(filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	db = d
	t.Cleanup(func() {
		db = old
		d.Close()
	})
	return d
}

func TestStockCategoriesSeededOnce(t *testing.T) {
	withTempCatsDB(t)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_categories`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(stockRules) {
		t.Fatalf("首次启动应写入 %d 个内置品类，实际 %d", len(stockRules), n)
	}
	// 删掉一个内置品类后，重新 seed 不应把它加回来。
	if _, err := db.Exec(`DELETE FROM stock_categories WHERE key='cat_food'`); err != nil {
		t.Fatal(err)
	}
	if err := seedStockCategories(db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_categories WHERE key='cat_food'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("用户删除的内置品类不应被启动种子加回，实际 count=%d", n)
	}
}

func TestCustomCategoryKeywordsInOCR(t *testing.T) {
	d := withTempCatsDB(t)
	// 加一个自定义品类，关键词「猫抓板」，排在内置之前。
	if _, err := d.Exec(`INSERT INTO stock_categories
		(key, name, keywords, shelf_days, sort_order, is_builtin, created_at, updated_at)
		VALUES('custom_scratch','猫抓板','猫抓板,猫玩具',120,-1,0,?,?)`, nowText(), nowText()); err != nil {
		t.Fatal(err)
	}
	r, ok := matchStockRule("猫抓板一个")
	if !ok || r.Key != "custom_scratch" || r.Name != "猫抓板" || r.ShelfDays != 120 {
		t.Fatalf("自定义品类应参与 OCR 匹配：key=%q name=%q days=%d ok=%v", r.Key, r.Name, r.ShelfDays, ok)
	}
	// 既有内置品类仍正常。
	r2, ok := matchStockRule("酱油")
	if !ok || r2.Key != "seasoning" {
		t.Fatalf("内置品类不应被自定义抢走：酱油 -> %q ok=%v", r2.Key, ok)
	}
}

func TestDeleteStockCategoryProtected(t *testing.T) {
	d := withTempCatsDB(t)
	// 给内置「药品」挂一项库存。
	if _, err := d.Exec(`INSERT INTO stock_items
		(name, category_key, category_name, unit, qty, created_at, updated_at)
		VALUES('维生素','medicine','药品','瓶',1,?,?)`, nowText(), nowText()); err != nil {
		t.Fatal(err)
	}
	var catID int64
	if err := d.QueryRow(`SELECT id FROM stock_categories WHERE key='medicine'`).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("DELETE", "/api/stock-categories/1", nil)
	req.SetPathValue("id", strconv.FormatInt(catID, 10))
	rec := httptest.NewRecorder()
	handleDeleteStockCategory(rec, req)
	if rec.Code != 400 {
		t.Fatalf("被引用品类删除应返回 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM stock_categories WHERE key='medicine'`).Scan(&n)
	if n != 1 {
		t.Fatal("被拒绝的删除不应影响品类")
	}
}

func TestRenameStockCategorySyncsItems(t *testing.T) {
	d := withTempCatsDB(t)
	if _, err := d.Exec(`INSERT INTO stock_items
		(name, category_key, category_name, unit, qty, created_at, updated_at)
		VALUES('鸡蛋','meat_egg_milk','肉蛋奶','个',10,?,?)`, nowText(), nowText()); err != nil {
		t.Fatal(err)
	}
	var catID int64
	if err := d.QueryRow(`SELECT id FROM stock_categories WHERE key='meat_egg_milk'`).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"蛋奶肉类"}`
	req := httptest.NewRequest("PUT", "/api/stock-categories/1", strings.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(catID, 10))
	rec := httptest.NewRecorder()
	handleUpdateStockCategory(rec, req)
	if rec.Code != 200 {
		t.Fatalf("改名应成功，实际 %d: %s", rec.Code, rec.Body.String())
	}
	var name string
	if err := d.QueryRow(`SELECT category_name FROM stock_items WHERE category_key='meat_egg_milk'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "蛋奶肉类" {
		t.Fatalf("改名应同步库存项显示名，实际 %q", name)
	}
	var metaName string
	_ = d.QueryRow(`SELECT name FROM stock_categories WHERE key='meat_egg_milk'`).Scan(&metaName)
	if metaName != "蛋奶肉类" {
		t.Fatalf("品类本身名称应更新，实际 %q", metaName)
	}
}

func TestCreateAndListStockCategory(t *testing.T) {
	withTempCatsDB(t)
	body := `{"name":"清洁用品","keywords":"洗衣液,洗洁精，消毒液","shelf_days":720}`
	req := httptest.NewRequest("POST", "/api/stock-categories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handleCreateStockCategory(rec, req)
	if rec.Code != 200 {
		t.Fatalf("新增品类应成功，实际 %d: %s", rec.Code, rec.Body.String())
	}
	var createdResp struct {
		Data struct {
			Key       string `json:"key"`
			Name      string `json:"name"`
			Keywords  string `json:"keywords"`
			ShelfDays int    `json:"shelf_days"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createdResp); err != nil {
		t.Fatal(err)
	}
	created := createdResp.Data
	if !strings.HasPrefix(created.Key, "custom_") || created.Name != "清洁用品" || created.ShelfDays != 720 {
		t.Fatalf("新增返回不符: %+v", created)
	}
	if created.Keywords != "洗衣液,洗洁精,消毒液" {
		t.Fatalf("关键词应统一为英文逗号分隔去重，实际 %q", created.Keywords)
	}
	// 列表里应能查到，且 item_count=0。
	listReq := httptest.NewRequest("GET", "/api/stock-categories", nil)
	listRec := httptest.NewRecorder()
	handleListStockCategories(listRec, listReq)
	var listResp struct {
		Data []StockCategory `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	list := listResp.Data
	found := false
	for _, c := range list {
		if c.Key == created.Key {
			found = true
			if c.ItemCount != 0 {
				t.Fatalf("新品类不应有库存项，实际 %d", c.ItemCount)
			}
		}
	}
	if !found {
		t.Fatal("新增品类未出现在列表中")
	}
}

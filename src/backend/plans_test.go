package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 计划单回归测试：守住「流水挂预算 → 计划单标记完成，但数据一行不丢」这条核心链路。
// 全程用 t.TempDir()，不碰 data/finance.db。

func seedPlanFixture(t *testing.T, d *sql.DB) (planID, allocID int64) {
	t.Helper()
	now := nowText()
	res, err := d.Exec(`INSERT INTO allocations(period, batch, name, budget_cents, created_at)
		VALUES('2026-11', 'first', '测试预算', 100000, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	allocID, _ = res.LastInsertId()
	res, err = d.Exec(`INSERT INTO plans(name, period, status, created_at, completed_at)
		VALUES('双十一采购计划', '2026-11', 'active', ?, '')`, now)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ = res.LastInsertId()
	if _, err := d.Exec(`INSERT INTO plan_allocations(plan_id, allocation_id) VALUES(?, ?)`, planID, allocID); err != nil {
		t.Fatal(err)
	}
	return planID, allocID
}

func planStatus(t *testing.T, d *sql.DB, id int64) (string, string) {
	t.Helper()
	var status, completedAt string
	if err := d.QueryRow(`SELECT status, completed_at FROM plans WHERE id = ?`, id).Scan(&status, &completedAt); err != nil {
		t.Fatal(err)
	}
	return status, completedAt
}

func TestEntryClearsPlansForAllocation(t *testing.T) {
	dir := t.TempDir()
	var err error
	db, err = openDB(filepath.Join(dir, "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	planID, allocID := seedPlanFixture(t, db)

	// 通过真实 HTTP handler 新增一条挂到该预算的流水
	body, _ := json.Marshal(map[string]any{
		"type": "expense", "amount": "88.50", "occurred_at": "2026-11-18T10:00:00",
		"allocation_id": allocID, "note": "计划内首笔支出",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/entries", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handleCreateEntry(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("新增流水失败: %d %s", rec.Code, rec.Body.String())
	}

	status, completedAt := planStatus(t, db, planID)
	if status != "completed" || completedAt == "" {
		t.Fatalf("流水挂预算后计划单应完成，实际 status=%s completed_at=%q", status, completedAt)
	}

	// 数据完整：计划单、关联、预算、流水都还在（clear 只改状态，不删任何一行）
	for _, tc := range []struct {
		sql  string
		id   int64
		want int
	}{
		{`SELECT COUNT(*) FROM plans WHERE id = ?`, planID, 1},
		{`SELECT COUNT(*) FROM plan_allocations WHERE plan_id = ?`, planID, 1},
		{`SELECT COUNT(*) FROM allocations WHERE id = ?`, allocID, 1},
		{`SELECT COUNT(*) FROM entries WHERE allocation_id = ?`, allocID, 1},
	} {
		var n int
		if err := db.QueryRow(tc.sql, tc.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != tc.want {
			t.Errorf("数据完整性受损: %s (id=%d) => %d, 期望 %d", tc.sql, tc.id, n, tc.want)
		}
	}
}

func TestPlanUnaffectedByUnrelatedEntry(t *testing.T) {
	dir := t.TempDir()
	var err error
	db, err = openDB(filepath.Join(dir, "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	planID, _ := seedPlanFixture(t, db)

	// 不挂预算的流水不应 clear 计划单
	body, _ := json.Marshal(map[string]any{
		"type": "expense", "amount": "12.00", "occurred_at": "2026-11-18T10:00:00", "note": "无关支出",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/entries", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handleCreateEntry(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("新增流水失败: %d %s", rec.Code, rec.Body.String())
	}
	if status, _ := planStatus(t, db, planID); status != "active" {
		t.Fatalf("无关流水不应改动计划单，实际 status=%s", status)
	}
}

func TestPlanReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	var err error
	db, err = openDB(filepath.Join(dir, "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	planID, allocID := seedPlanFixture(t, db)

	// 先 clear，再手动重开
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := clearPlansForAllocation(tx, allocID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if status, _ := planStatus(t, db, planID); status != "completed" {
		t.Fatalf("前置 clear 失败，status=%s", status)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/reopen", nil)
	req.SetPathValue("id", itoa(planID)) // 模拟 ServeMux 的路径参数绑定
	rec := httptest.NewRecorder()
	handleReopenPlan(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("重开失败: %d %s", rec.Code, rec.Body.String())
	}
	status, completedAt := planStatus(t, db, planID)
	if status != "active" || completedAt != "" {
		t.Fatalf("重开后应 active 且清除完成时间，实际 status=%s completed_at=%q", status, completedAt)
	}
	// 计划、关联、预算仍在
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM plan_allocations WHERE plan_id = ?`, planID).Scan(&n)
	if n != 1 {
		t.Fatalf("重开不应删除关联，实际 %d 条", n)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

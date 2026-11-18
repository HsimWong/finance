package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
)

// 计划单（v4）：预算通过 plan_allocations 多对多挂到计划单上，allocations 表结构不动。
// 流水挂到某个预算时，含该预算且仍为 active 的计划单会被「clear」——
// 只写状态与完成时间，计划单/关联/预算/流水全部保留，历史可追溯。

// clearPlansForAllocation 把关联到该预算、尚未完成的计划单全部标记完成。
// 必须在写入流水的同一个事务里调用，保证「流水发生 → 计划完成」原子生效。
func clearPlansForAllocation(tx *sql.Tx, allocationID int64) error {
	_, err := tx.Exec(`UPDATE plans
		SET status = 'completed', completed_at = ?
		WHERE status = 'active'
		  AND id IN (SELECT plan_id FROM plan_allocations WHERE allocation_id = ?)`,
		nowText(), allocationID)
	return err
}

// queryPlans 列出计划单，并把关联的预算明细一并带回（含预算已花进度）。
func queryPlans(period, status string) ([]Plan, error) {
	conds := []string{"1=1"}
	args := []any{}
	if period != "" {
		conds = append(conds, "p.period = ?")
		args = append(args, period)
	}
	if status == "active" || status == "completed" {
		conds = append(conds, "p.status = ?")
		args = append(args, status)
	}
	rows, err := db.Query(`SELECT p.id, p.name, p.period, p.note, p.status, p.created_at, p.completed_at
		FROM plans p WHERE `+strings.Join(conds, " AND ")+`
		ORDER BY p.period DESC, p.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	out := make([]Plan, 0)
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Name, &p.Period, &p.Note, &p.Status, &p.CreatedAt, &p.CompletedAt); err != nil {
			rows.Close()
			return nil, err
		}
		p.Allocations = []Allocation{}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close() // 单连接：先关闭再查下一批，避免互相等待

	if len(out) == 0 {
		return out, nil
	}

	// 一次性取全部预算，按 id 建索引；比逐计划单查省连接占用。
	allAllocs, err := queryAllocations("")
	if err != nil {
		return nil, err
	}
	allocByID := make(map[int64]Allocation, len(allAllocs))
	for _, a := range allAllocs {
		allocByID[a.ID] = a
	}

	// 关联关系：plan_id -> allocation_id 列表
	linkRows, err := db.Query(`SELECT plan_id, allocation_id FROM plan_allocations ORDER BY allocation_id`)
	if err != nil {
		return nil, err
	}
	links := make(map[int64][]int64)
	for linkRows.Next() {
		var pid, aid int64
		if err := linkRows.Scan(&pid, &aid); err != nil {
			linkRows.Close()
			return nil, err
		}
		links[pid] = append(links[pid], aid)
	}
	if err := linkRows.Err(); err != nil {
		linkRows.Close()
		return nil, err
	}
	linkRows.Close()

	for i := range out {
		for _, aid := range links[out[i].ID] {
			a, ok := allocByID[aid]
			if !ok {
				continue // 预算已删但关联残留（正常由外键级联清掉），跳过即可
			}
			out[i].Allocations = append(out[i].Allocations, a)
			out[i].BudgetCents += a.BudgetCents
			out[i].SpentCents += a.SpentCents
		}
		out[i].Budget = FormatCents(out[i].BudgetCents)
		out[i].Spent = FormatCents(out[i].SpentCents)
		out[i].RemainingCents = out[i].BudgetCents - out[i].SpentCents
		out[i].Remaining = FormatCents(out[i].RemainingCents)
		if out[i].BudgetCents > 0 {
			out[i].Progress = float64(out[i].SpentCents) / float64(out[i].BudgetCents)
		}
	}
	return out, nil
}

func replacePlanAllocations(tx *sql.Tx, planID int64, ids []int64) error {
	if _, err := tx.Exec(`DELETE FROM plan_allocations WHERE plan_id = ?`, planID); err != nil {
		return err
	}
	seen := make(map[int64]bool, len(ids))
	for _, aid := range ids {
		if aid <= 0 || seen[aid] {
			continue
		}
		seen[aid] = true
		if _, err := tx.Exec(`INSERT INTO plan_allocations(plan_id, allocation_id) VALUES(?, ?)`, planID, aid); err != nil {
			return fmt.Errorf("关联预算 #%d 失败（预算不存在？）: %w", aid, err)
		}
	}
	return nil
}

// ---------- HTTP ----------

func handleListPlans(w http.ResponseWriter, r *http.Request) {
	items, err := queryPlans(r.URL.Query().Get("period"), r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, 500, "查询计划单失败: %v", err)
		return
	}
	writeOK(w, items)
}

func handleGetPlan(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	items, err := queryPlans("", "")
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	for _, p := range items {
		if p.ID == id {
			writeOK(w, p)
			return
		}
	}
	writeErr(w, 404, "计划单不存在")
}

func handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var in planInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	if err := validatePlan(&in); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO plans(name, period, note, status, created_at, completed_at)
		VALUES(?, ?, ?, 'active', ?, '')`, strings.TrimSpace(in.Name), in.Period, in.Note, nowText())
	if err != nil {
		writeErr(w, 400, "新增失败: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	if err := replacePlanAllocations(tx, id, in.AllocationIDs); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in planInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	if err := validatePlan(&in); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	// 编辑不碰 status/completed_at：完成是独立动作，避免改资料把 clear 结果抹掉。
	res, err := tx.Exec(`UPDATE plans SET name = ?, period = ?, note = ? WHERE id = ?`,
		strings.TrimSpace(in.Name), in.Period, in.Note, id)
	if err != nil {
		writeErr(w, 400, "更新失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "计划单不存在")
		return
	}
	if err := replacePlanAllocations(tx, id, in.AllocationIDs); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleDeletePlan(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	// 只删计划单与其关联；预算和流水都不动。
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM plan_allocations WHERE plan_id = ?`, id); err != nil {
		writeErr(w, 500, "清理关联失败: %v", err)
		return
	}
	res, err := tx.Exec(`DELETE FROM plans WHERE id = ?`, id)
	if err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "计划单不存在")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleClearPlan(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	res, err := db.Exec(`UPDATE plans SET status = 'completed', completed_at = ?
		WHERE id = ? AND status = 'active'`, nowText(), id)
	if err != nil {
		writeErr(w, 500, "标记完成失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 已完成或不存在都按幂等成功处理
		writeOK(w, map[string]any{"id": id, "status": "completed"})
		return
	}
	writeOK(w, map[string]any{"id": id, "status": "completed"})
}

func handleReopenPlan(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	// 手动重开是显式操作：清掉完成时间，但不动任何预算/流水。
	res, err := db.Exec(`UPDATE plans SET status = 'active', completed_at = ''
		WHERE id = ? AND status = 'completed'`, id)
	if err != nil {
		writeErr(w, 500, "重新打开失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "计划单不存在或已是进行中")
		return
	}
	writeOK(w, map[string]any{"id": id, "status": "active"})
}

func validatePlan(in *planInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("请填写计划单名称")
	}
	if in.Period != "" && (len(in.Period) != 7 || in.Period[4] != '-') {
		return fmt.Errorf("月份需为 YYYY-MM 格式（可留空）")
	}
	return nil
}

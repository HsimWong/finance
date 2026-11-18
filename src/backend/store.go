package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const entrySelect = `SELECT e.id, e.type, e.amount_cents, e.occurred_at,
	e.member_id, COALESCE(m.name, ''),
	e.category_id, COALESCE(c.name, ''),
	e.allocation_id, COALESCE(a.name, ''),
	e.screenshot_id, e.source, e.note, e.created_at, e.updated_at
FROM entries e
LEFT JOIN members m ON m.id = e.member_id
LEFT JOIN categories c ON c.id = e.category_id
LEFT JOIN allocations a ON a.id = e.allocation_id`

// scanEntries 先读完并关闭 rows，再补标签，避免单连接下嵌套查询互相等待。
func scanEntries(rows *sql.Rows) ([]Entry, error) {
	list := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		var memberID, categoryID, allocationID, screenshotID sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Type, &e.AmountCents, &e.OccurredAt,
			&memberID, &e.MemberName, &categoryID, &e.CategoryName,
			&allocationID, &e.AllocationName, &screenshotID,
			&e.Source, &e.Note, &e.CreatedAt, &e.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		e.MemberID = nullIntPtr(memberID)
		e.CategoryID = nullIntPtr(categoryID)
		e.AllocationID = nullIntPtr(allocationID)
		e.ScreenshotID = nullIntPtr(screenshotID)
		e.Amount = FormatCents(e.AmountCents)
		e.Tags = []Tag{}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(list) > 0 {
		ids := make([]int64, len(list))
		for i, e := range list {
			ids[i] = e.ID
		}
		tagMap, err := queryTagsForEntries(ids)
		if err != nil {
			return nil, err
		}
		for i := range list {
			if ts, ok := tagMap[list[i].ID]; ok {
				list[i].Tags = ts
			}
		}
	}
	return list, nil
}

func queryTagsForEntries(ids []int64) (map[int64][]Tag, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(`SELECT et.entry_id, t.id, t.name FROM entry_tags et
		JOIN tags t ON t.id = et.tag_id
		WHERE et.entry_id IN (`+ph+`) ORDER BY t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[int64][]Tag)
	for rows.Next() {
		var eid int64
		var t Tag
		if err := rows.Scan(&eid, &t.ID, &t.Name); err != nil {
			return nil, err
		}
		m[eid] = append(m[eid], t)
	}
	return m, rows.Err()
}

func queryMembers() ([]Member, error) {
	rows, err := db.Query(`SELECT id, name, created_at FROM members ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Member, 0)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Name, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func queryCategories() ([]Category, error) {
	rows, err := db.Query(`SELECT id, parent_id, name, kind, sort_order FROM categories
		ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Category, 0)
	for rows.Next() {
		var c Category
		var pid sql.NullInt64
		if err := rows.Scan(&c.ID, &pid, &c.Name, &c.Kind, &c.SortOrder); err != nil {
			return nil, err
		}
		c.ParentID = nullIntPtr(pid)
		out = append(out, c)
	}
	return out, rows.Err()
}

func queryTags() ([]Tag, error) {
	rows, err := db.Query(`SELECT id, name FROM tags ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Tag, 0)
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func queryAllocations(period string) ([]Allocation, error) {
	where := "1=1"
	args := []any{}
	if period != "" {
		where = "a.period = ?"
		args = append(args, period)
	}
	rows, err := db.Query(`SELECT a.id, a.period, a.batch, a.name, a.category_id,
		COALESCE(c.name, ''), a.budget_cents,
		COALESCE((SELECT SUM(e.amount_cents) FROM entries e
			WHERE e.allocation_id = a.id AND e.type = 'expense'), 0),
		a.note, a.created_at
	FROM allocations a LEFT JOIN categories c ON c.id = a.category_id
	WHERE `+where+` ORDER BY a.period DESC, CASE a.batch WHEN 'first' THEN 0 ELSE 1 END, a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Allocation, 0)
	for rows.Next() {
		var a Allocation
		var cid sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Period, &a.Batch, &a.Name, &cid,
			&a.CategoryName, &a.BudgetCents, &a.SpentCents, &a.Note, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.CategoryID = nullIntPtr(cid)
		a.Budget = FormatCents(a.BudgetCents)
		a.Spent = FormatCents(a.SpentCents)
		a.RemainingCents = a.BudgetCents - a.SpentCents
		a.Remaining = FormatCents(a.RemainingCents)
		if a.BudgetCents > 0 {
			a.Progress = float64(a.SpentCents) / float64(a.BudgetCents)
		}
		switch {
		case a.SpentCents == 0:
			a.Status = "未动用"
		case a.SpentCents < a.BudgetCents:
			a.Status = "进行中"
		case a.SpentCents == a.BudgetCents:
			a.Status = "已用完"
		default:
			a.Status = "超额"
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// expandCategoryIDs 递归取分类及其所有子孙 id。
func expandCategoryIDs(root int64) []int64 {
	rows, err := db.Query(`WITH RECURSIVE sub(id) AS (
		SELECT ? UNION ALL
		SELECT c.id FROM categories c JOIN sub ON c.parent_id = sub.id
	) SELECT id FROM sub`, root)
	if err != nil {
		return []int64{root}
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return []int64{root}
	}
	return ids
}

type entryFilter struct {
	where string
	args  []any
}

func buildFilter(v url.Values) entryFilter {
	conds := []string{"1=1"}
	args := []any{}
	add := func(cond string, val any) {
		conds = append(conds, cond)
		args = append(args, val)
	}
	if t := v.Get("type"); t == "income" || t == "expense" {
		add("e.type = ?", t)
	}
	if s := v.Get("member_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			add("e.member_id = ?", id)
		}
	}
	if s := v.Get("category_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			if v.Get("include_subtree") == "1" {
				ids := expandCategoryIDs(id)
				ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
				conds = append(conds, "e.category_id IN ("+ph+")")
				for _, x := range ids {
					args = append(args, x)
				}
			} else {
				add("e.category_id = ?", id)
			}
		}
	}
	if s := v.Get("tag_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			add("EXISTS (SELECT 1 FROM entry_tags etf WHERE etf.entry_id = e.id AND etf.tag_id = ?)", id)
		}
	}
	if s := v.Get("allocation_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			add("e.allocation_id = ?", id)
		}
	}
	if v.Get("uncategorized") == "1" {
		conds = append(conds, "e.category_id IS NULL")
	}
	if from := v.Get("from"); from != "" {
		add("substr(e.occurred_at, 1, 10) >= ?", from)
	}
	if to := v.Get("to"); to != "" {
		add("substr(e.occurred_at, 1, 10) <= ?", to)
	}
	if q := strings.TrimSpace(v.Get("q")); q != "" {
		add("e.note LIKE ?", "%"+q+"%")
	}
	return entryFilter{where: strings.Join(conds, " AND "), args: args}
}

func normalizeTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nowText()
	}
	switch len(s) {
	case 10:
		return s + "T00:00:00"
	case 16:
		return s + ":00"
	}
	return s
}

func validateEntry(in *entryInput) error {
	if in.Type != "income" && in.Type != "expense" {
		return fmt.Errorf("类型必须是 income 或 expense")
	}
	if in.Amount <= 0 {
		return fmt.Errorf("金额必须大于 0")
	}
	in.OccurredAt = normalizeTime(in.OccurredAt)
	if in.Source == "" {
		in.Source = "manual"
	}
	return nil
}

func replaceTags(tx *sql.Tx, entryID int64, tagIDs []int64) error {
	if _, err := tx.Exec(`DELETE FROM entry_tags WHERE entry_id = ?`, entryID); err != nil {
		return err
	}
	for _, tid := range tagIDs {
		// 无效 tag_id 直接忽略，避免外键报错
		if _, err := tx.Exec(`INSERT OR IGNORE INTO entry_tags(entry_id, tag_id)
			SELECT ?, id FROM tags WHERE id = ?`, entryID, tid); err != nil {
			return err
		}
	}
	return nil
}

type reportRow struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	TotalCents int64  `json:"total_cents"`
	Total      string `json:"total"`
	Count      int    `json:"count"`
}

func queryReport(v url.Values) ([]reportRow, error) {
	group := v.Get("group_by")
	if group == "" {
		group = "category"
	}
	f := buildFilter(v)
	var q string
	switch group {
	case "category": // 按顶级领域汇总（含子分类）
		q = `WITH RECURSIVE cat_tree(id, root_name) AS (
			SELECT id, name FROM categories WHERE parent_id IS NULL
			UNION ALL
			SELECT c.id, ct.root_name FROM categories c JOIN cat_tree ct ON c.parent_id = ct.id
		)
		SELECT COALESCE(ct.root_name, '未分类'), SUM(e.amount_cents), COUNT(*)
		FROM entries e LEFT JOIN cat_tree ct ON ct.id = e.category_id
		WHERE ` + f.where + ` GROUP BY COALESCE(ct.root_name, '未分类') ORDER BY SUM(e.amount_cents) DESC`
	case "category_detail":
		q = `SELECT COALESCE(c.name, '未分类'), SUM(e.amount_cents), COUNT(*)
		FROM entries e LEFT JOIN categories c ON c.id = e.category_id
		WHERE ` + f.where + ` GROUP BY COALESCE(c.name, '未分类') ORDER BY SUM(e.amount_cents) DESC`
	case "member":
		q = `SELECT COALESCE(m.name, '未指定'), SUM(e.amount_cents), COUNT(*)
		FROM entries e LEFT JOIN members m ON m.id = e.member_id
		WHERE ` + f.where + ` GROUP BY COALESCE(m.name, '未指定') ORDER BY SUM(e.amount_cents) DESC`
	case "tag":
		q = `SELECT COALESCE(t.name, '无标签'), SUM(e.amount_cents), COUNT(*)
		FROM entries e
		LEFT JOIN entry_tags et ON et.entry_id = e.id
		LEFT JOIN tags t ON t.id = et.tag_id
		WHERE ` + f.where + ` GROUP BY COALESCE(t.name, '无标签') ORDER BY SUM(e.amount_cents) DESC`
	case "month":
		q = `SELECT substr(e.occurred_at, 1, 7), SUM(e.amount_cents), COUNT(*)
		FROM entries e WHERE ` + f.where + ` GROUP BY substr(e.occurred_at, 1, 7) ORDER BY 1 DESC`
	case "day":
		q = `SELECT substr(e.occurred_at, 1, 10), SUM(e.amount_cents), COUNT(*)
		FROM entries e WHERE ` + f.where + ` GROUP BY substr(e.occurred_at, 1, 10) ORDER BY 1 DESC`
	default:
		return nil, fmt.Errorf("不支持的 group_by：%s", group)
	}
	rows, err := db.Query(q, f.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]reportRow, 0)
	for rows.Next() {
		var r reportRow
		if err := rows.Scan(&r.Key, &r.TotalCents, &r.Count); err != nil {
			return nil, err
		}
		r.Label = r.Key
		r.Total = FormatCents(r.TotalCents)
		out = append(out, r)
	}
	return out, rows.Err()
}

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxScreenshotBytes = 10 << 20 // 10 MiB

func registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/meta", handleMeta)

	mux.HandleFunc("POST /api/members", handleCreateMember)
	mux.HandleFunc("PATCH /api/members/{id}", handleUpdateMember)
	mux.HandleFunc("DELETE /api/members/{id}", handleDeleteMember)
	mux.HandleFunc("POST /api/categories", handleCreateCategory)
	mux.HandleFunc("DELETE /api/categories/{id}", handleDeleteCategory)
	mux.HandleFunc("POST /api/tags", handleCreateTag)

	mux.HandleFunc("GET /api/screenshots", handleListScreenshots)
	mux.HandleFunc("POST /api/screenshots", handleUploadScreenshot)
	mux.HandleFunc("GET /api/screenshots/{id}/image", handleScreenshotImage)
	mux.HandleFunc("DELETE /api/screenshots/{id}", handleDeleteScreenshot)

	mux.HandleFunc("GET /api/entries", handleListEntries)
	mux.HandleFunc("POST /api/entries", handleCreateEntry)
	mux.HandleFunc("GET /api/entries/{id}", handleGetEntry)
	mux.HandleFunc("PUT /api/entries/{id}", handleUpdateEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", handleDeleteEntry)
	mux.HandleFunc("PATCH /api/entries/{id}/classify", handleClassifyEntry)
	mux.HandleFunc("GET /api/alerts", handleAlerts)

	mux.HandleFunc("GET /api/allocations", handleListAllocations)
	mux.HandleFunc("POST /api/allocations", handleCreateAllocation)
	mux.HandleFunc("PUT /api/allocations/{id}", handleUpdateAllocation)
	mux.HandleFunc("DELETE /api/allocations/{id}", handleDeleteAllocation)

	mux.HandleFunc("GET /api/plans", handleListPlans)
	mux.HandleFunc("GET /api/plans/{id}", handleGetPlan)
	mux.HandleFunc("POST /api/plans", handleCreatePlan)
	mux.HandleFunc("PUT /api/plans/{id}", handleUpdatePlan)
	mux.HandleFunc("DELETE /api/plans/{id}", handleDeletePlan)
	mux.HandleFunc("POST /api/plans/{id}/clear", handleClearPlan)
	mux.HandleFunc("POST /api/plans/{id}/reopen", handleReopenPlan)

	mux.HandleFunc("GET /api/report", handleReport)
	mux.HandleFunc("GET /api/dashboard", handleDashboard)

	mux.HandleFunc("GET /api/ocr/status", handleOCRStatus)
	mux.HandleFunc("POST /api/ocr/recognize", handleOCRRecognize)
	mux.HandleFunc("POST /api/ocr/parse-text", handleOCRParseText)

	mux.HandleFunc("GET /api/stock/summary", handleStockSummary)
	mux.HandleFunc("GET /api/stock/items", handleListStockItems)
	mux.HandleFunc("POST /api/stock/items", handleCreateStockItem)
	mux.HandleFunc("PUT /api/stock/items/{id}", handleUpdateStockItem)
	mux.HandleFunc("DELETE /api/stock/items/{id}", handleDeleteStockItem)
	mux.HandleFunc("POST /api/stock/items/{id}/consume", handleConsumeStock)
	mux.HandleFunc("POST /api/stock/items/{id}/waste", handleWasteStock)
	mux.HandleFunc("GET /api/stock/movements", handleStockMovements)

	mux.HandleFunc("GET /api/stock-categories", handleListStockCategories)
	mux.HandleFunc("POST /api/stock-categories", handleCreateStockCategory)
	mux.HandleFunc("PUT /api/stock-categories/{id}", handleUpdateStockCategory)
	mux.HandleFunc("DELETE /api/stock-categories/{id}", handleDeleteStockCategory)
}

// ---------- 基础数据 ----------

func handleMeta(w http.ResponseWriter, r *http.Request) {
	members, err := queryMembers()
	if err != nil {
		writeErr(w, 500, "读取成员失败: %v", err)
		return
	}
	cats, err := queryCategories()
	if err != nil {
		writeErr(w, 500, "读取分类失败: %v", err)
		return
	}
	tags, err := queryTags()
	if err != nil {
		writeErr(w, 500, "读取标签失败: %v", err)
		return
	}
	dynamicRules := loadStockRules()
	rules := make([]map[string]any, 0, len(dynamicRules))
	for _, r := range dynamicRules {
		rules = append(rules, map[string]any{"key": r.Key, "name": r.Name, "shelf_days": r.ShelfDays})
	}
	writeOK(w, map[string]any{
		"members": members, "categories": cats, "tags": tags, "stock_rules": rules,
	})
}

func handleCreateMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "请提供成员姓名")
		return
	}
	res, err := db.Exec(`INSERT INTO members(name, created_at) VALUES(?, ?)`, strings.TrimSpace(in.Name), nowText())
	if err != nil {
		writeErr(w, 400, "新增失败（可能重名）: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	writeOK(w, Member{ID: id, Name: strings.TrimSpace(in.Name), CreatedAt: nowText()})
}

func handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "请提供成员姓名")
		return
	}
	if _, err := db.Exec(`UPDATE members SET name = ? WHERE id = ?`, strings.TrimSpace(in.Name), id); err != nil {
		writeErr(w, 400, "更新失败（可能重名）: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id, "name": strings.TrimSpace(in.Name)})
}

func handleDeleteMember(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM entries WHERE member_id = ?`, id).Scan(&n)
	if n > 0 {
		writeErr(w, 400, "该成员已有流水记录，不能删除")
		return
	}
	if _, err := db.Exec(`DELETE FROM members WHERE id = ?`, id); err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ParentID  *int64 `json:"parent_id"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		SortOrder int    `json:"sort_order"`
	}
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "请提供分类名称")
		return
	}
	if in.Kind == "" {
		in.Kind = "expense"
	}
	if in.Kind != "expense" && in.Kind != "income" && in.Kind != "both" {
		writeErr(w, 400, "kind 只能是 expense / income / both")
		return
	}
	res, err := db.Exec(`INSERT INTO categories(parent_id, name, kind, sort_order) VALUES(?, ?, ?, ?)`,
		ptrArg(in.ParentID), strings.TrimSpace(in.Name), in.Kind, in.SortOrder)
	if err != nil {
		writeErr(w, 400, "新增失败: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	writeOK(w, Category{ID: id, ParentID: in.ParentID, Name: strings.TrimSpace(in.Name), Kind: in.Kind, SortOrder: in.SortOrder})
}

func handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var n int
	_ = db.QueryRow(`SELECT (SELECT COUNT(*) FROM categories WHERE parent_id = ?)
		+ (SELECT COUNT(*) FROM entries WHERE category_id = ?)
		+ (SELECT COUNT(*) FROM allocations WHERE category_id = ?)`, id, id, id).Scan(&n)
	if n > 0 {
		writeErr(w, 400, "该分类有子分类或已被流水/预算引用，不能删除")
		return
	}
	if _, err := db.Exec(`DELETE FROM categories WHERE id = ?`, id); err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "请提供标签名称")
		return
	}
	res, err := db.Exec(`INSERT INTO tags(name) VALUES(?)`, strings.TrimSpace(in.Name))
	if err != nil {
		writeErr(w, 400, "新增失败（可能重名）: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	writeOK(w, Tag{ID: id, Name: strings.TrimSpace(in.Name)})
}

// ---------- 截图 ----------

func readUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxScreenshotBytes+2<<20)
	if err := r.ParseMultipartForm(maxScreenshotBytes + 2<<20); err != nil {
		return nil, "", fmt.Errorf("解析上传失败（文件过大？）: %v", err)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", fmt.Errorf("缺少文件字段 file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxScreenshotBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("读取文件失败: %v", err)
	}
	return data, header.Filename, nil
}

func saveScreenshot(data []byte, filename, note string) (int64, string, error) {
	if len(data) == 0 {
		return 0, "", fmt.Errorf("文件为空")
	}
	if len(data) > maxScreenshotBytes {
		return 0, "", fmt.Errorf("图片不能超过 10 MiB")
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return 0, "", fmt.Errorf("只支持图片文件（jpg / png / webp / gif）")
	}
	res, err := db.Exec(`INSERT INTO screenshots(filename, mime, size, data, status, note, uploaded_at)
		VALUES(?, ?, ?, ?, 'pending', ?, ?)`, filename, mime, len(data), data, note, nowText())
	if err != nil {
		return 0, "", err
	}
	id, _ := res.LastInsertId()
	return id, mime, nil
}

func handleUploadScreenshot(w http.ResponseWriter, r *http.Request) {
	data, filename, err := readUpload(w, r)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	note := r.FormValue("note")
	id, mime, err := saveScreenshot(data, filename, note)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeOK(w, Screenshot{
		ID: id, Filename: filename, Mime: mime, Size: int64(len(data)),
		Status: "pending", Note: note, UploadedAt: nowText(),
	})
}

// ---------- OCR 识别 ----------

func recognizeResponse(p ParsedBill, screenshotID int64, errMsg string) map[string]any {
	if errMsg != "" {
		p.Warnings = append(p.Warnings, errMsg)
	}
	return map[string]any{
		"screenshot_id": screenshotID,
		"engine":        p.Engine,
		"raw_text":      p.RawText,
		"lines":         p.Lines,
		"suggestion":    p.Suggestion,
		"items":         p.Items,
		"confidence":    p.Confidence,
		"warnings":      p.Warnings,
		"error":         errMsg,
	}
}

func handleOCRStatus(w http.ResponseWriter, r *http.Request) {
	writeOK(w, map[string]any{
		"available": ocr.Available, "engine": ocr.Engine, "hint": ocr.Hint,
	})
}

func handleOCRRecognize(w http.ResponseWriter, r *http.Request) {
	var data []byte
	var shotID int64
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		d, filename, err := readUpload(w, r)
		if err != nil {
			writeErr(w, 400, "%v", err)
			return
		}
		data = d
		id, _, err := saveScreenshot(data, filename, "")
		if err != nil {
			writeErr(w, 400, "%v", err)
			return
		}
		shotID = id
	} else {
		var in struct {
			ScreenshotID int64 `json:"screenshot_id"`
		}
		if err := decodeJSON(r, &in); err != nil || in.ScreenshotID == 0 {
			writeErr(w, 400, "请提供 screenshot_id，或直接上传图片文件")
			return
		}
		var filename string
		if err := db.QueryRow(`SELECT data, filename FROM screenshots WHERE id = ?`, in.ScreenshotID).Scan(&data, &filename); err != nil {
			writeErr(w, 404, "截图不存在")
			return
		}
		shotID = in.ScreenshotID
	}

	if !ocr.Available {
		writeOK(w, recognizeResponse(ParseBill("none", nil), shotID,
			"OCR 引擎不可用："+ocr.Hint+"。可改用「粘贴文本解析」。"))
		return
	}
	lines, engine, err := ocr.Recognize(data)
	if err != nil {
		writeOK(w, recognizeResponse(ParseBill("none", nil), shotID, err.Error()))
		return
	}
	parsed := ParseBill(engine, lines)
	_, _ = db.Exec(`UPDATE screenshots SET status = 'parsed' WHERE id = ?`, shotID)
	writeOK(w, recognizeResponse(parsed, shotID, ""))
}

func handleOCRParseText(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text         string `json:"text"`
		ScreenshotID int64  `json:"screenshot_id"`
	}
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeErr(w, 400, "请提供账单文本内容")
		return
	}
	parsed := ParseBillFromText(in.Text)
	writeOK(w, recognizeResponse(parsed, in.ScreenshotID, ""))
}

func handleListScreenshots(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT s.id, s.filename, s.mime, s.size, s.status, s.note, s.uploaded_at,
		EXISTS(SELECT 1 FROM entries e WHERE e.screenshot_id = s.id)
		FROM screenshots s ORDER BY s.uploaded_at DESC, s.id DESC LIMIT 200`)
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	defer rows.Close()
	out := make([]Screenshot, 0)
	for rows.Next() {
		var s Screenshot
		var has bool
		if err := rows.Scan(&s.ID, &s.Filename, &s.Mime, &s.Size, &s.Status, &s.Note, &s.UploadedAt, &has); err != nil {
			writeErr(w, 500, "扫描失败: %v", err)
			return
		}
		s.HasEntry = has
		out = append(out, s)
	}
	writeOK(w, out)
}

func handleScreenshotImage(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var mime string
	var data []byte
	if err := db.QueryRow(`SELECT mime, data FROM screenshots WHERE id = ?`, id).Scan(&mime, &data); err != nil {
		writeErr(w, 404, "截图不存在")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func handleDeleteScreenshot(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	if _, err := db.Exec(`DELETE FROM screenshots WHERE id = ?`, id); err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

// ---------- 流水 ----------

func handleListEntries(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	f := buildFilter(v)
	limit := 200
	if s := v.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	offset := 0
	if s := v.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}
	rows, err := db.Query(entrySelect+` WHERE `+f.where+`
		ORDER BY e.occurred_at DESC, e.id DESC LIMIT ? OFFSET ?`, append(f.args, limit, offset)...)
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	items, err := scanEntries(rows)
	if err != nil {
		writeErr(w, 500, "读取流水失败: %v", err)
		return
	}
	var total int
	var sum int64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(e.amount_cents), 0) FROM entries e WHERE `+f.where, f.args...).Scan(&total, &sum); err != nil {
		writeErr(w, 500, "统计失败: %v", err)
		return
	}
	writeOK(w, map[string]any{
		"items": items, "total": total,
		"sum_cents": sum, "sum": FormatCents(sum),
		"limit": limit, "offset": offset,
	})
}

func handleGetEntry(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	rows, err := db.Query(entrySelect+` WHERE e.id = ?`, id)
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	items, err := scanEntries(rows)
	if err != nil {
		writeErr(w, 500, "读取失败: %v", err)
		return
	}
	if len(items) == 0 {
		writeErr(w, 404, "流水不存在")
		return
	}
	writeOK(w, items[0])
}

func handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	var in entryInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	if err := validateEntry(&in); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO entries(type, amount_cents, occurred_at, member_id,
		category_id, allocation_id, screenshot_id, source, note, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Type, int64(in.Amount), in.OccurredAt, ptrArg(in.MemberID), ptrArg(in.CategoryID),
		ptrArg(in.AllocationID), ptrArg(in.ScreenshotID), in.Source, in.Note, nowText(), nowText())
	if err != nil {
		writeErr(w, 400, "新增失败（检查成员/分类/预算/截图 id 是否有效）: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	if err := replaceTags(tx, id, in.TagIDs); err != nil {
		writeErr(w, 500, "保存标签失败: %v", err)
		return
	}
	if in.SyncStock {
		if err := saveAutoStockItems(tx, id, in.StockItems); err != nil {
			writeErr(w, 500, "家庭用品自动入库失败: %v", err)
			return
		}
	}
	// 流水挂到预算 → 含该预算的计划单本事务内标记完成（只改状态，数据全保留）
	if in.AllocationID != nil {
		if err := clearPlansForAllocation(tx, *in.AllocationID); err != nil {
			writeErr(w, 500, "更新计划单状态失败: %v", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	rows, err := db.Query(entrySelect+` WHERE e.id = ?`, id)
	if err != nil {
		writeErr(w, 500, "回读失败: %v", err)
		return
	}
	items, err := scanEntries(rows)
	if err != nil || len(items) == 0 {
		writeErr(w, 500, "回读失败: %v", err)
		return
	}
	writeOK(w, items[0])
}

func handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in entryInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	if err := validateEntry(&in); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE entries SET type=?, amount_cents=?, occurred_at=?, member_id=?,
		category_id=?, allocation_id=?, screenshot_id=?, source=?, note=?, updated_at=? WHERE id=?`,
		in.Type, int64(in.Amount), in.OccurredAt, ptrArg(in.MemberID), ptrArg(in.CategoryID),
		ptrArg(in.AllocationID), ptrArg(in.ScreenshotID), in.Source, in.Note, nowText(), id)
	if err != nil {
		writeErr(w, 400, "更新失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "流水不存在")
		return
	}
	if err := replaceTags(tx, id, in.TagIDs); err != nil {
		writeErr(w, 500, "保存标签失败: %v", err)
		return
	}
	if in.SyncStock {
		if err := saveAutoStockItems(tx, id, in.StockItems); err != nil {
			writeErr(w, 500, "家庭用品自动入库失败: %v", err)
			return
		}
	}
	// 编辑流水时若重新关联到预算，同样 clear 含该预算的计划单。
	// 去掉预算关联不会回滚已完成状态——clear 是已发生的事实，保持历史完整。
	if in.AllocationID != nil {
		if err := clearPlansForAllocation(tx, *in.AllocationID); err != nil {
			writeErr(w, 500, "更新计划单状态失败: %v", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	rows, err := db.Query(entrySelect+` WHERE e.id = ?`, id)
	if err != nil {
		writeErr(w, 500, "回读失败: %v", err)
		return
	}
	items, err := scanEntries(rows)
	if err != nil || len(items) == 0 {
		writeErr(w, 500, "回读失败")
		return
	}
	writeOK(w, items[0])
}

func handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, "事务失败: %v", err)
		return
	}
	defer tx.Rollback()
	// 先清掉由这条流水自动生成的库存项，手工维护的库存保留
	if _, err := tx.Exec(`DELETE FROM stock_items WHERE entry_id = ? AND auto_created = 1`, id); err != nil {
		writeErr(w, 500, "清理关联库存失败: %v", err)
		return
	}
	res, err := tx.Exec(`DELETE FROM entries WHERE id = ?`, id)
	if err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "流水不存在")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleClassifyEntry(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in struct {
		CategoryID *int64  `json:"category_id"`
		TagIDs     []int64 `json:"tag_ids"`
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
	res, err := tx.Exec(`UPDATE entries SET category_id = ?, updated_at = ? WHERE id = ?`, ptrArg(in.CategoryID), nowText(), id)
	if err != nil {
		writeErr(w, 400, "归类失败（分类 id 无效？）: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "流水不存在")
		return
	}
	if err := replaceTags(tx, id, in.TagIDs); err != nil {
		writeErr(w, 500, "保存标签失败: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "提交失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id, "category_id": in.CategoryID})
}

func handleAlerts(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	v.Set("uncategorized", "1")
	f := buildFilter(v)
	rows, err := db.Query(entrySelect+` WHERE `+f.where+` ORDER BY e.occurred_at DESC, e.id DESC LIMIT 200`, f.args...)
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	items, err := scanEntries(rows)
	if err != nil {
		writeErr(w, 500, "读取失败: %v", err)
		return
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM entries WHERE category_id IS NULL`).Scan(&count)
	writeOK(w, map[string]any{"items": items, "count": count})
}

// ---------- 预算 ----------

func handleListAllocations(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	items, err := queryAllocations(period)
	if err != nil {
		writeErr(w, 500, "查询失败: %v", err)
		return
	}
	writeOK(w, items)
}

func handleCreateAllocation(w http.ResponseWriter, r *http.Request) {
	var in allocationInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	if err := validateAllocation(&in); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	res, err := db.Exec(`INSERT INTO allocations(period, batch, name, category_id, budget_cents, note, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, in.Period, in.Batch, strings.TrimSpace(in.Name),
		ptrArg(in.CategoryID), int64(in.Budget), in.Note, nowText())
	if err != nil {
		writeErr(w, 400, "新增失败（分类 id 无效？）: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	writeOK(w, map[string]any{"id": id})
}

func handleUpdateAllocation(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	var in allocationInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误: %v", err)
		return
	}
	if err := validateAllocation(&in); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	res, err := db.Exec(`UPDATE allocations SET period=?, batch=?, name=?, category_id=?, budget_cents=?, note=? WHERE id=?`,
		in.Period, in.Batch, strings.TrimSpace(in.Name), ptrArg(in.CategoryID), int64(in.Budget), in.Note, id)
	if err != nil {
		writeErr(w, 400, "更新失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "预算不存在")
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func handleDeleteAllocation(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "id")
	if err != nil {
		writeErr(w, 400, "无效 id")
		return
	}
	if _, err := db.Exec(`DELETE FROM allocations WHERE id = ?`, id); err != nil {
		writeErr(w, 500, "删除失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func validateAllocation(in *allocationInput) error {
	if len(in.Period) != 7 || in.Period[4] != '-' {
		return fmt.Errorf("period 需为 YYYY-MM 格式")
	}
	if in.Batch != "first" && in.Batch != "mid" {
		return fmt.Errorf("batch 只能是 first（月初）或 mid（月中）")
	}
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("请填写预算名称")
	}
	if in.Budget <= 0 {
		return fmt.Errorf("预算金额必须大于 0")
	}
	return nil
}

// ---------- 报表 / 总览 ----------

func handleReport(w http.ResponseWriter, r *http.Request) {
	rows, err := queryReport(r.URL.Query())
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	var sum int64
	for _, row := range rows {
		sum += row.TotalCents
	}
	writeOK(w, map[string]any{
		"group_by": r.URL.Query().Get("group_by"),
		"rows":     rows, "sum_cents": sum, "sum": FormatCents(sum),
	})
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if len(period) != 7 {
		period = monthText()
	}
	var incomeCents, expenseCents int64
	if err := db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN type='income' THEN amount_cents ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type='expense' THEN amount_cents ELSE 0 END), 0)
		FROM entries WHERE substr(occurred_at, 1, 7) = ?`, period).Scan(&incomeCents, &expenseCents); err != nil {
		writeErr(w, 500, "汇总失败: %v", err)
		return
	}
	allocs, err := queryAllocations(period)
	if err != nil {
		writeErr(w, 500, "读取预算失败: %v", err)
		return
	}
	var allocatedCents, usedCents int64
	for _, a := range allocs {
		allocatedCents += a.BudgetCents
		usedCents += a.SpentCents
	}
	var uncategorized, entryCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM entries WHERE category_id IS NULL`).Scan(&uncategorized)
	_ = db.QueryRow(`SELECT COUNT(*) FROM entries WHERE substr(occurred_at, 1, 7) = ?`, period).Scan(&entryCount)

	rv := url.Values{}
	rv.Set("group_by", "category")
	rv.Set("type", "expense")
	rv.Set("from", period+"-01")
	rv.Set("to", period+"-31")
	breakdown, err := queryReport(rv)
	if err != nil {
		breakdown = []reportRow{}
	}

	rows, err := db.Query(entrySelect + ` ORDER BY e.occurred_at DESC, e.id DESC LIMIT 6`)
	if err != nil {
		writeErr(w, 500, "读取最近流水失败: %v", err)
		return
	}
	recent, err := scanEntries(rows)
	if err != nil {
		writeErr(w, 500, "读取最近流水失败: %v", err)
		return
	}

	var allocationRate, usageRate float64
	if incomeCents > 0 {
		allocationRate = float64(allocatedCents) / float64(incomeCents)
	}
	if allocatedCents > 0 {
		usageRate = float64(usedCents) / float64(allocatedCents)
	}

	writeOK(w, map[string]any{
		"period":              period,
		"income_cents":        incomeCents,
		"income":              FormatCents(incomeCents),
		"expense_cents":       expenseCents,
		"expense":             FormatCents(expenseCents),
		"balance_cents":       incomeCents - expenseCents,
		"balance":             FormatCents(incomeCents - expenseCents),
		"allocated_cents":     allocatedCents,
		"allocated":           FormatCents(allocatedCents),
		"used_cents":          usedCents,
		"used":                FormatCents(usedCents),
		"remaining_cents":     allocatedCents - usedCents,
		"remaining":           FormatCents(allocatedCents - usedCents),
		"unallocated_cents":   incomeCents - allocatedCents,
		"unallocated":         FormatCents(incomeCents - allocatedCents),
		"allocation_rate":     allocationRate,
		"usage_rate":          usageRate,
		"uncategorized_count": uncategorized,
		"entry_count":         entryCount,
		"allocations":         allocs,
		"category_breakdown":  breakdown,
		"recent_entries":      recent,
	})
}

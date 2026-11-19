package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Money 以「分」为单位存储；JSON 中按「元」输入。
type Money int64

func (m *Money) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*m = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	c, err := YuanToCents(s)
	if err != nil {
		return err
	}
	*m = Money(c)
	return nil
}

// YuanToCents 把「元」字符串精确转成「分」，不经过浮点数。
func YuanToCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimPrefix(s, "¥")
	s = strings.TrimPrefix(s, "￥")
	if s == "" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	var yuan int64
	if parts[0] != "" {
		v, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("金额格式不正确：%s", s)
		}
		yuan = v
	}
	var frac int64
	if len(parts) == 2 {
		f := parts[1]
		for _, r := range f {
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("金额格式不正确：%s", s)
			}
		}
		if len(f) == 0 {
			f = "00"
		}
		if len(f) == 1 {
			f += "0"
		}
		v, _ := strconv.ParseInt(f[:2], 10, 64)
		frac = v
		if len(f) > 2 && f[2] >= '5' {
			frac++
		}
	}
	total := yuan*100 + frac
	if neg {
		total = -total
	}
	return total, nil
}

func FormatCents(c int64) string {
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

// ---------- 数据结构 ----------

type Member struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type Category struct {
	ID        int64  `json:"id"`
	ParentID  *int64 `json:"parent_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SortOrder int    `json:"sort_order"`
}

type Tag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Screenshot struct {
	ID         int64  `json:"id"`
	Filename   string `json:"filename"`
	Mime       string `json:"mime"`
	Size       int64  `json:"size"`
	Status     string `json:"status"`
	Note       string `json:"note"`
	UploadedAt string `json:"uploaded_at"`
	HasEntry   bool   `json:"has_entry"`
}

type Entry struct {
	ID             int64  `json:"id"`
	Type           string `json:"type"`
	AmountCents    int64  `json:"amount_cents"`
	Amount         string `json:"amount"`
	OccurredAt     string `json:"occurred_at"`
	MemberID       *int64 `json:"member_id"`
	MemberName     string `json:"member_name"`
	CategoryID     *int64 `json:"category_id"`
	CategoryName   string `json:"category_name"`
	AllocationID   *int64 `json:"allocation_id"`
	AllocationName string `json:"allocation_name"`
	ScreenshotID   *int64 `json:"screenshot_id"`
	Source         string `json:"source"`
	Note           string `json:"note"`
	Tags           []Tag  `json:"tags"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type Allocation struct {
	ID             int64   `json:"id"`
	Period         string  `json:"period"`
	Batch          string  `json:"batch"`
	Name           string  `json:"name"`
	CategoryID     *int64  `json:"category_id"`
	CategoryName   string  `json:"category_name"`
	BudgetCents    int64   `json:"budget_cents"`
	Budget         string  `json:"budget"`
	SpentCents     int64   `json:"spent_cents"`
	Spent          string  `json:"spent"`
	RemainingCents int64   `json:"remaining_cents"`
	Remaining      string  `json:"remaining"`
	Progress       float64 `json:"progress"`
	Status         string  `json:"status"`
	Note           string  `json:"note"`
	CreatedAt      string  `json:"created_at"`
}

// Plan 计划单：预算多对多关联，status=completed 表示已被流水 clear。
type Plan struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Period         string       `json:"period"`
	Note           string       `json:"note"`
	Status         string       `json:"status"`
	CreatedAt      string       `json:"created_at"`
	CompletedAt    string       `json:"completed_at"`
	Allocations    []Allocation `json:"allocations"`
	BudgetCents    int64        `json:"budget_cents"`
	Budget         string       `json:"budget"`
	SpentCents     int64        `json:"spent_cents"`
	Spent          string       `json:"spent"`
	RemainingCents int64        `json:"remaining_cents"`
	Remaining      string       `json:"remaining"`
	Progress       float64      `json:"progress"`
}

type planInput struct {
	Name          string  `json:"name"`
	Period        string  `json:"period"`
	Note          string  `json:"note"`
	AllocationIDs []int64 `json:"allocation_ids"`
}

type entryInput struct {
	Type         string  `json:"type"`
	Amount       Money   `json:"amount"`
	OccurredAt   string  `json:"occurred_at"`
	MemberID     *int64  `json:"member_id"`
	CategoryID   *int64  `json:"category_id"`
	AllocationID *int64  `json:"allocation_id"`
	ScreenshotID *int64  `json:"screenshot_id"`
	Source       string  `json:"source"`
	Note         string  `json:"note"`
	TagIDs     []int64 `json:"tag_ids"`
	// SyncStock 显式声明本次是否同步库存，不传/false 表示完全不碰库存。
	// 为 true 时以 StockItems 为准重建本流水的自动入库项；传空数组即清空自动项。
	// 用户手工维护的库存（auto_created=0）任何时候都不受影响。
	SyncStock  bool              `json:"sync_stock"`
	StockItems []stockItemInput  `json:"stock_items"`
}

type allocationInput struct {
	Period     string `json:"period"`
	Batch      string `json:"batch"`
	Name       string `json:"name"`
	CategoryID *int64 `json:"category_id"`
	Budget     Money  `json:"budget"`
	Note       string `json:"note"`
}

// ---------- HTTP 工具 ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]any{"ok": false, "error": fmt.Sprintf(format, args...)})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
}

func idParam(r *http.Request, key string) (int64, error) {
	return strconv.ParseInt(r.PathValue(key), 10, 64)
}

func nowText() string   { return time.Now().Format("2006-01-02T15:04:05") }
func todayText() string { return time.Now().Format("2006-01-02") }
func monthText() string { return time.Now().Format("2006-01") }

func nullIntPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func ptrArg(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

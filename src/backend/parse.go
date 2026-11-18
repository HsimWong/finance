package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ---------- 识别结果结构 ----------

type OCRLine struct {
	Text  string       `json:"text"`
	Score float64      `json:"score"`
	Box   [][2]float64 `json:"box,omitempty"`
}

type EntrySuggestion struct {
	Type            string             `json:"type"`
	Amount          string             `json:"amount"`
	AmountCents     int64              `json:"amount_cents"`
	OccurredAt      string             `json:"occurred_at"`
	Merchant        string             `json:"merchant"`
	CategoryID      *int64             `json:"category_id"`
	CategoryName    string             `json:"category_name"`
	TagIDs          []int64            `json:"tag_ids"`
	Note            string             `json:"note"`
	FieldConfidence map[string]float64 `json:"field_confidence"`
}

type ParsedItem struct {
	Name         string  `json:"name"`
	Qty          float64 `json:"qty"`
	Unit         string  `json:"unit"`
	Amount       string  `json:"amount"`
	AmountCents  int64   `json:"amount_cents"`
	CategoryKey  string  `json:"category_key"`
	CategoryName string  `json:"category_name"`
	ExpiryDate   string  `json:"expiry_date"`
	IsHousehold  bool    `json:"is_household"`
	Confidence   float64 `json:"confidence"`
}

type ParsedBill struct {
	Engine     string          `json:"engine"`
	RawText    string          `json:"raw_text"`
	Lines      []OCRLine       `json:"lines"`
	Suggestion EntrySuggestion `json:"suggestion"`
	Items      []ParsedItem    `json:"items"`
	Confidence float64         `json:"confidence"`
	Warnings   []string        `json:"warnings"`
}

// ---------- 库存品类规则 ----------

type stockRule struct {
	Key       string
	Name      string
	Keywords  []string
	ShelfDays int
}

// 顺序即优先级：更具体的品类放前面，避免「猫粮」被粮油规则抢走。
var stockRules = []stockRule{
	{"cat_food", "猫罐头", []string{"猫罐头", "猫粮", "猫条", "猫砂", "猫零食", "冻干", "猫", "宠物"}, 365},
	{"medicine", "药品", []string{
		"布洛芬", "感冒", "退烧", "创可贴", "碘伏", "维生素", "板蓝根", "消炎", "药", "口罩", "酒精",
		// 常见慢病 / 常用药（剂型词在 medicineForms 统一兜底，这里只列药名）
		"别嘌醇", "二甲双胍", "阿司匹林", "氯雷他定", "奥美拉唑", "阿莫西林", "头孢", "蒙脱石",
		"对乙酰氨基酚", "硝苯地平", "氨氯地平", "他汀", "胰岛素", "叶酸", "左氧氟沙星", "甲硝唑",
		"布地奈德", "沙丁胺醇", "炉甘石", "开塞露", "眼药",
	}, 365},
	{"fruit_veg", "瓜果蔬菜", []string{"白菜", "菠菜", "青菜", "生菜", "西红柿", "番茄", "黄瓜", "土豆", "胡萝卜", "苹果", "香蕉", "橙", "橘子", "葡萄", "西瓜", "梨", "桃", "草莓", "蓝莓", "大葱", "香葱", "生姜", "大蒜", "蒜", "辣椒", "蘑菇", "香菇", "金针菇", "西兰花", "芹菜", "茄子", "冬瓜", "南瓜", "菜", "水果", "蔬菜", "果蔬"}, 7},
	{"meat_egg_milk", "肉蛋奶", []string{"鸡蛋", "鸭蛋", "鹌鹑蛋", "牛奶", "酸奶", "奶酪", "猪肉", "牛肉", "羊肉", "鸡肉", "鸡胸", "排骨", "五花", "培根", "香肠", "虾", "三文鱼", "带鱼", "鱼", "肉", "蛋", "奶"}, 14},
	// 调料要先于粮油匹配，否则「酱油 / 蚝油」会被粮油的单字「油」抢走
	{"seasoning", "调料", []string{"食盐", "酱油", "陈醋", "白醋", "料酒", "蚝油", "白糖", "味精", "鸡精", "花椒", "八角", "辣椒酱", "豆瓣酱", "胡椒", "淀粉", "番茄酱", "调味", "调料", "酱料"}, 365},
	{"grain_oil", "粮油", []string{"大米", "米饭", "面粉", "面条", "挂面", "食用油", "花生油", "橄榄油", "菜籽油", "调和油", "色拉油", "玉米油", "葵花籽油", "杂粮", "小米", "燕麦", "红豆", "绿豆", "玉米"}, 180},
}

// 药品剂型词。具体药名没法穷举（「别嘌醇缓释胶囊」「厄贝沙坦氢氯噻嗪片」……），
// 但剂型是极强的信号，命中即判为药品，避免每来一种新药就要加一次关键词。
var medicineForms = []string{
	"胶囊", "缓释", "肠溶", "分散片", "咀嚼片", "泡腾片", "含片", "片剂",
	"颗粒剂", "冲剂", "口服液", "合剂", "滴眼液", "滴鼻液", "软膏", "乳膏",
	"凝胶", "喷雾剂", "气雾剂", "吸入剂", "栓剂", "注射液", "糖浆", "丸剂",
	"贴膏", "膏药", "散剂", "搽剂",
}

// 含剂型词但并非药品的常见商品，避免「咖啡胶囊」「薯片」被当成药。
var medicineFormExclusions = []string{
	"咖啡胶囊", "胶囊咖啡", "薯片", "虾片", "姜片", "蒜片", "肉片", "鱼片",
	"鱼丸", "肉丸", "牛肉丸", "麦片", "奶片", "颗粒面", "玉米粒", "果冻",
}

func matchStockRule(name string) (stockRule, bool) {
	for _, r := range loadStockRules() {
		for _, kw := range r.Keywords {
			if strings.Contains(name, kw) {
				return r, true
			}
		}
	}
	if !containsAny(name, medicineFormExclusions) && containsAny(name, medicineForms) {
		if r, ok := findStockRuleByKey("medicine"); ok {
			return r, true
		}
	}
	return stockRule{}, false
}

// ---------- 商户 / 分类规则 ----------

type merchantRule struct {
	Keywords []string
	Merchant string
	Category string
	Tags     []string
}

var merchantRules = []merchantRule{
	{[]string{"美团", "饿了么", "外卖", "跑腿"}, "外卖平台", "外卖", nil},
	{[]string{"盒马", "永辉", "沃尔玛", "大润发", "物美", "超市", "生鲜", "菜市场", "果蔬", "钱大妈", "农贸市场"}, "超市 / 生鲜", "买菜生鲜", []string{"囤货"}},
	{[]string{"药房", "药店", "大药房", "医院", "诊所"}, "药店 / 医院", "药品", nil},
	{[]string{"宠物", "猫罐头", "猫粮", "猫砂"}, "宠物用品", "猫粮猫罐头", []string{"猫咪"}},
	// 汽车类放在「交通出行」之前：加油/停车/保养/车险 都属于汽车消费，不能被打车规则抢走
	// 维修规则必须排在保养之前：「汽车维修」「更换刹车片」这类不能再落到保养
	{[]string{"汽车维修", "车辆维修", "维修", "修理厂", "汽修", "钣金", "喷漆", "刹车片", "刹车", "换胎", "补胎", "轮胎", "发动机", "变速箱", "配件"}, "汽车维修", "维修", nil},
	{[]string{"途虎", "4S店", "4S", "汽车保养", "车辆保养", "保养套餐", "换机油", "机油", "滤芯", "洗车", "年检"}, "汽车养护", "保养", nil},
	{[]string{"车险", "交强险", "商业险", "车辆保险", "车船税"}, "汽车保险", "保险", nil},
	{[]string{"加油", "中石化", "中石油", "壳牌", "油站", "充电桩"}, "加油 / 充电", "加油", nil},
	{[]string{"停车", "ETC", "高速通行", "过路费"}, "停车通行", "停车费", nil},
	{[]string{"滴滴", "高德打车", "出租车", "地铁", "公交"}, "交通出行", "打车", nil},
	{[]string{"房租", "物业", "水电", "燃气", "水费", "电费"}, "居住缴费", "房租", []string{"房租", "周期固定"}},
	{[]string{"淘宝", "京东", "拼多多", "天猫", "抖音商城"}, "电商平台", "日用品", nil},
	{[]string{"星巴克", "瑞幸", "咖啡", "奶茶", "餐饮", "餐厅", "饭店"}, "餐饮消费", "外出就餐", nil},
}

var (
	reNumber     = regexp.MustCompile(`\d+(?:[.,]\d{1,2})?`)
	reMoneyToken = regexp.MustCompile(`[¥￥]?\s*(-?\d+(?:[.,]\d{1,2})?)`)
	reFullDate   = regexp.MustCompile(`(20\d{2})\s*[-/.年]\s*(\d{1,2})\s*[-/.月]\s*(\d{1,2})`)
	reShortDate  = regexp.MustCompile(`(\d{1,2})\s*月\s*(\d{1,2})\s*日`)
	reClock      = regexp.MustCompile(`(\d{1,2}):(\d{2})`)
	reQtyTail    = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(kg|KG|g|G|斤|两|盒|瓶|袋|包|箱|个|件|份|支|条|只|块|束|把|罐|桶|升|ml|ML|L)?\s*$`)
)

var totalKeywords = []string{"实付", "合计", "总计", "应付", "付款金额", "支付金额", "订单金额", "实付款", "交易金额", "总金额", "收款金额"}
var incomeKeywords = []string{"收款", "入账", "转入", "退款", "收入", "已存入", "到账", "余额增加", "转入金额", "+¥", "+￥"}
var expenseKeywords = []string{"付款", "支付", "消费", "支出", "实付", "-¥", "-￥", "已支付", "支付成功", "付款成功", "缴费"}
var itemExcludeKeywords = []string{"合计", "总计", "实付", "应付", "支付成功", "付款成功", "优惠", "折扣", "找零", "订单号", "订单", "交易时间", "付款时间", "备注", "会员", "收款方", "付款方式", "余额", "手续费", "积分", "小计", "单价", "数量", "商品名称", "微信支付", "支付宝", "账单详情", "共", "笔", "成功", "已完", "服务费", "配送费", "包装费", "红包"}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// normalizeOCRText 把全角数字/标点折成半角，去掉 OCR 常见噪声。
func normalizeOCRText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0xFF10 && r <= 0xFF19:
			return rune('0' + (r - 0xFF10))
		case r == 0xFF0E:
			return '.'
		case r == 0xFF0C:
			return ','
		case r == 0xFF1A:
			return ':'
		case r == 0xFFE5 || r == 0xFFE6:
			return '¥'
		case r == 0x3000:
			return ' '
		case unicode.IsSpace(r):
			return ' '
		}
		return r
	}, s)
}

func parseMoneyToken(s string) (int64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	s = strings.TrimPrefix(s, "¥")
	s = strings.TrimPrefix(s, "+")
	s = strings.TrimPrefix(s, "-")
	c, err := YuanToCents(s)
	if err != nil || c <= 0 {
		return 0, false
	}
	return c, true
}

type moneyCandidate struct {
	cents int64
	score float64
	text  string
}

// pickTotalAmount 综合关键词、货币符号、位置、数值大小给金额候选打分。
func pickTotalAmount(lines []string) (moneyCandidate, bool) {
	var best moneyCandidate
	found := false
	var all []moneyCandidate
	for i, line := range lines {
		// 日期/时间行里的数字（年、月、日、时分）不是金额，整体降权
		dateLine := reFullDate.MatchString(line) || reShortDate.MatchString(line) || reClock.MatchString(line)
		for _, m := range reMoneyToken.FindAllString(line, -1) {
			cents, ok := parseMoneyToken(m)
			if !ok || cents > 10000000 { // 上限 10 万元，过滤订单号等长数字碎片
				continue
			}
			c := moneyCandidate{cents: cents, text: line}
			if dateLine && !strings.Contains(m, "¥") && !containsAny(line, totalKeywords) {
				c.score -= 0.6
			}
			if containsAny(line, totalKeywords) {
				c.score += 0.65
			}
			if strings.Contains(line, "¥") {
				c.score += 0.25
			}
			if strings.Contains(line, "-¥") || strings.Contains(line, "+¥") {
				c.score += 0.15
			}
			if containsAny(line, []string{"支付成功", "付款成功", "交易成功", "收款成功"}) {
				c.score += 0.2
			}
			if float64(cents) > 1000 { // 大于 10 元更像总额
				c.score += 0.05
			}
			if i < 6 {
				c.score += 0.05
			}
			all = append(all, c)
		}
	}
	for _, c := range all {
		if !found || c.score > best.score || (c.score == best.score && c.cents > best.cents) {
			best, found = c, true
		}
	}
	return best, found
}

func pickDate(lines []string, fallbackDate string) (string, float64) {
	date := fallbackDate
	clock := "12:00"
	conf := 0.3
	for _, line := range lines {
		if m := reFullDate.FindStringSubmatch(line); m != nil {
			y, _ := strconv.Atoi(m[1])
			mo, _ := strconv.Atoi(m[2])
			d, _ := strconv.Atoi(m[3])
			if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
				date = fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
				conf = 0.95
			}
		} else if m := reShortDate.FindStringSubmatch(line); m != nil && conf < 0.9 {
			mo, _ := strconv.Atoi(m[1])
			d, _ := strconv.Atoi(m[2])
			if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
				date = fmt.Sprintf("%s-%02d-%02d", fallbackDate[:4], mo, d)
				conf = 0.8
			}
		}
		if m := reClock.FindStringSubmatch(line); m != nil {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			if h < 24 && mi < 60 {
				clock = fmt.Sprintf("%02d:%02d", h, mi)
			}
		}
	}
	return date + "T" + clock, conf
}

func guessDirection(text string) (string, float64) {
	in, out := 0, 0
	for _, k := range incomeKeywords {
		in += strings.Count(text, k)
	}
	for _, k := range expenseKeywords {
		out += strings.Count(text, k)
	}
	if in > out {
		return "income", 0.85
	}
	if out > 0 {
		return "expense", 0.85
	}
	return "expense", 0.4
}

func matchMerchant(text string) (merchantRule, bool) {
	for _, r := range merchantRules {
		if containsAny(text, r.Keywords) {
			return r, true
		}
	}
	return merchantRule{}, false
}

// parseItemLine 从单行里拆出「品名 + 数量单位 + 金额」，只保留家庭用品。
func parseItemLine(line string, fallbackDate string) (ParsedItem, bool) {
	if containsAny(line, itemExcludeKeywords) {
		return ParsedItem{}, false
	}
	locs := reNumber.FindAllStringIndex(line, -1)
	if len(locs) == 0 {
		return ParsedItem{}, false
	}
	last := locs[len(locs)-1]
	cents, ok := parseMoneyToken(line[last[0]:last[1]])
	if !ok || cents > 10000000 {
		return ParsedItem{}, false
	}
	prefix := strings.TrimSpace(line[:last[0]])
	prefix = strings.TrimRight(prefix, "¥ ￥x*×:：.-")
	prefix = strings.TrimSpace(prefix)
	runes := []rune(prefix)
	if len(runes) < 2 {
		return ParsedItem{}, false
	}

	name := prefix
	qty := 1.0
	unit := "件"
	if m := reQtyTail.FindStringSubmatch(prefix); m != nil {
		q, err := strconv.ParseFloat(m[1], 64)
		if err == nil && q > 0 && q < 10000 {
			qty = q
			if m[2] != "" {
				unit = m[2]
			}
			name = strings.TrimSpace(strings.TrimSuffix(prefix, m[0]))
		}
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 2 {
		name = prefix
		qty, unit = 1, "件"
	}

	rule, hit := matchStockRule(name)
	if !hit {
		return ParsedItem{}, false
	}
	date := fallbackDate
	if len(date) > 10 {
		date = date[:10]
	}
	expiry := ""
	if t, err := time.Parse("2006-01-02", date); err == nil {
		expiry = t.AddDate(0, 0, rule.ShelfDays).Format("2006-01-02")
	}
	return ParsedItem{
		Name: strings.TrimSpace(name), Qty: qty, Unit: unit,
		Amount: FormatCents(cents), AmountCents: cents,
		CategoryKey: rule.Key, CategoryName: rule.Name,
		ExpiryDate: expiry, IsHousehold: true, Confidence: 0.75,
	}, true
}

// ParseBill 把 OCR 文本行解析成一条可复核的流水草稿 + 待入库商品明细。
func ParseBill(engine string, lines []OCRLine) ParsedBill {
	sorted := append([]OCRLine(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool {
		yi, yj := 0.0, 0.0
		if len(sorted[i].Box) > 0 {
			yi = sorted[i].Box[0][1]
		}
		if len(sorted[j].Box) > 0 {
			yj = sorted[j].Box[0][1]
		}
		if yi == 0 && yj == 0 {
			return false
		}
		return yi < yj
	})

	texts := make([]string, 0, len(sorted))
	scoreSum := 0.0
	for _, l := range sorted {
		t := strings.TrimSpace(normalizeOCRText(l.Text))
		if t != "" {
			texts = append(texts, t)
			scoreSum += l.Score
		}
	}
	rawText := strings.Join(texts, "\n")
	warnings := []string{}

	amount, amountOK := pickTotalAmount(texts)
	typeText, dirConf := guessDirection(rawText)
	fallbackDate := time.Now().Format("2006-01-02")
	occurredAt, dateConf := pickDate(texts, fallbackDate)
	merchant, merchantOK := matchMerchant(rawText)

	tagNames := []string{}
	categoryConf := 0.3
	note := ""

	suggestionCategory := ""
	if merchantOK {
		note = merchant.Merchant
		suggestionCategory = merchant.Category
		tagNames = append(tagNames, merchant.Tags...)
		categoryConf = 0.8
	}

	items := []ParsedItem{}
	itemLineCount := 0
	for _, t := range texts {
		it, ok := parseItemLine(t, strings.Split(occurredAt, "T")[0])
		if ok {
			items = append(items, it)
			itemLineCount++
		}
	}
	if len(items) > 0 {
		// 有家庭用品时，账单主分类优先归到囤货采购，标签带上囤货
		suggestionCategory = "囤货采购"
		categoryConf = 0.85
		if !containsAny(strings.Join(tagNames, ","), []string{"囤货"}) {
			tagNames = append(tagNames, "囤货")
		}
		note = strings.TrimSpace(note + " · 含家庭用品 " + strconv.Itoa(len(items)) + " 项")
	}
	if typeText == "income" && suggestionCategory == "" {
		suggestionCategory = "其他收入"
	}
	if suggestionCategory == "" {
		suggestionCategory = "其他支出"
	}

	sug := EntrySuggestion{
		Type:        typeText,
		Amount:      FormatCents(amount.cents),
		AmountCents: amount.cents,
		OccurredAt:  occurredAt,
		Merchant: func() string {
			if merchantOK {
				return merchant.Merchant
			}
			return ""
		}(),
		CategoryName: suggestionCategory,
		CategoryID:   findCategoryID(suggestionCategory),
		TagIDs:       findTagIDs(tagNames),
		Note:         note,
		FieldConfidence: map[string]float64{
			"amount": amount.score, "occurred_at": dateConf,
			"type": dirConf, "category": categoryConf,
		},
	}
	if !amountOK {
		sug.Amount, sug.AmountCents = "", 0
		sug.FieldConfidence["amount"] = 0
		warnings = append(warnings, "未识别到金额，请手动填写")
	} else if amount.score < 0.5 {
		warnings = append(warnings, "金额为推测值，请重点核对")
	}
	if dateConf < 0.5 {
		warnings = append(warnings, "未识别到日期，已默认今天")
	}
	if len(items) > 0 {
		warnings = append(warnings, fmt.Sprintf("识别到 %d 项家庭用品，确认后将自动入库；保质期按品类默认值预填，可修改", len(items)))
	} else {
		warnings = append(warnings, "未识别到家庭用品明细，本次不会自动入库")
	}
	if len(texts) > 0 && scoreSum/float64(len(texts)) < 0.7 {
		warnings = append(warnings, "OCR 整体置信度偏低，建议逐项复核")
	}
	if engine == "text" {
		warnings = append(warnings, "当前为文本解析模式（未调用图像识别）")
	}
	_ = itemLineCount

	overall := 0.0
	if amountOK {
		overall = (sug.FieldConfidence["amount"] + dateConf + dirConf + categoryConf) / 4
	}
	if overall > 1 {
		overall = 1
	}
	return ParsedBill{
		Engine: engine, RawText: rawText, Lines: sorted,
		Suggestion: sug, Items: items,
		Confidence: overall, Warnings: warnings,
	}
}

// ParseBillFromText 供「粘贴文本」兜底模式复用同一套解析规则。
func ParseBillFromText(text string) ParsedBill {
	lines := []OCRLine{}
	for _, t := range strings.Split(text, "\n") {
		if strings.TrimSpace(t) != "" {
			lines = append(lines, OCRLine{Text: t, Score: 1})
		}
	}
	return ParseBill("text", lines)
}

// findCategoryID 按名称找分类。同名时优先「叶子分类」（没有子分类的那个），
// 这样「保险」会命中 汽车›保险 而不是顶级 保险；顶级分类只在没有更具体的同名项时才用。
func findCategoryID(name string) *int64 {
	var id int64
	if err := db.QueryRow(`SELECT c.id FROM categories c WHERE c.name = ?
		ORDER BY (SELECT COUNT(*) FROM categories k WHERE k.parent_id = c.id) ASC, c.id LIMIT 1`, name).Scan(&id); err != nil {
		return nil
	}
	return &id
}

func findTagIDs(names []string) []int64 {
	ids := []int64{}
	seen := map[int64]bool{}
	for _, n := range names {
		var id int64
		if err := db.QueryRow(`SELECT id FROM tags WHERE name = ?`, n).Scan(&id); err == nil && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids
}

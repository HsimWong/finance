package main

import "testing"

// 这个测试守住「药品品类」的两个坑：
//  1. 具体药名不可能穷举（别嘌醇缓释胶囊 / 厄贝沙坦分散片 ……），必须靠剂型词兜底；
//  2. 含剂型词但不是药的商品（咖啡胶囊、薯片、麦片）不能被误判成药品。
func TestMatchStockRuleMedicine(t *testing.T) {
	wantMedicine := []string{
		"别嘌醇缓释胶囊", "厄贝沙坦分散片", "蒙脱石散", "布洛芬缓释胶囊",
		"阿托伐他汀钙片剂", "头孢克肟颗粒剂", "复方甘草口服液", "氧氟沙星滴眼液",
		"红霉素软膏", "维生素C泡腾片",
	}
	for _, name := range wantMedicine {
		r, ok := matchStockRule(name)
		if !ok || r.Key != "medicine" {
			t.Errorf("%q 应识别为药品，实际 key=%q ok=%v", name, r.Key, ok)
		}
	}

	// 剂型词出现在非药品商品上时不能误判
	wantNotMedicine := []string{"咖啡胶囊", "乐事薯片", "原味麦片", "牛肉丸", "奶片", "鱼丸"}
	for _, name := range wantNotMedicine {
		if r, ok := matchStockRule(name); ok && r.Key == "medicine" {
			t.Errorf("%q 不应识别为药品", name)
		}
	}

	// 既有品类不能被新规则抢走
	cases := map[string]string{
		"鸡蛋":     "meat_egg_milk",
		"纯牛奶":    "meat_egg_milk",
		"白菜":     "fruit_veg",
		"酱油":     "seasoning",
		"花生油":    "grain_oil",
		"猫罐头":    "cat_food",
		"维生素泡腾片": "medicine",
	}
	for name, want := range cases {
		r, ok := matchStockRule(name)
		if !ok || r.Key != want {
			t.Errorf("%q 应为 %s，实际 %q ok=%v", name, want, r.Key, ok)
		}
	}
}

// 显式选定的品类必须以 key 为准，不能被「按名字没猜出来」覆盖成「其他」。
// 这正是「别嘌醇缓释胶囊显示成其他」的根因，用测试钉死。
func TestResolveStockCategory(t *testing.T) {
	cases := []struct {
		key, catName, itemName string
		wantKey, wantName      string
		why                    string
	}{
		{"medicine", "", "别嘌醇缓释胶囊", "medicine", "药品", "显式选药品，名字不含「药」也要认"},
		{"medicine", "其他", "别嘌醇缓释胶囊", "medicine", "药品", "历史脏数据（key 对、name 错）要纠正"},
		{"", "", "别嘌醇缓释胶囊", "medicine", "药品", "未选品类时按剂型词推断"},
		{"", "", "鸡蛋", "meat_egg_milk", "肉蛋奶", "未选品类时按名称推断"},
		{"fruit_veg", "", "苹果", "fruit_veg", "瓜果蔬菜", "显式选择优先于名称推断"},
		{"other", "", "鸡蛋", "other", "其他", "显式选其他就尊重用户，不再猜"},
		{"", "", "某个没见过的物件", "other", "其他", "都推断不出时落其他"},
	}
	for _, c := range cases {
		k, n := resolveStockCategory(c.key, c.catName, c.itemName)
		if k != c.wantKey || n != c.wantName {
			t.Errorf("%s：resolveStockCategory(%q,%q,%q) = %q/%q，期望 %q/%q",
				c.why, c.key, c.catName, c.itemName, k, n, c.wantKey, c.wantName)
		}
	}
}

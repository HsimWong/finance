package main

import (
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var db *sql.DB
var ocr *OCRProvider

// detectRoot 依据源码位置推断项目根目录，使 go run 不受当前工作目录影响。
func detectRoot() string {
	// 优先按可执行文件位置推断（<root>/bin/finance → <root>）：
	// 交叉编译出的二进制 runtime.Caller 指向的是编译机的源码路径，
	// 之前就因此在服务器上找错根目录、丢了 .env 和静态文件。
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{filepath.Dir(dir), dir} {
			if _, err := os.Stat(filepath.Join(cand, "src", "frontend")); err == nil {
				return filepath.Clean(cand)
			}
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	}
	cwd, _ := os.Getwd()
	return cwd
}

func main() {
	rootFlag := flag.String("root", "", "项目根目录（默认自动推断）")
	envFlag := flag.String("env", "", "配置文件路径（默认 <root>/.env）")
	addrFlag := flag.String("addr", "", "监听地址，优先级最高，例如 127.0.0.1:8081")
	pythonFlag := flag.String("ocr-python", "", "本地 OCR 使用的 Python 解释器（默认 python）")
	snapFlag := flag.String("snapshot-interval", "30m", "运行时自动快照间隔（数据有变化才备份，0 关闭）")
	flag.Parse()

	snapInterval := time.Duration(0)
	if *snapFlag != "0" && *snapFlag != "off" {
		var e error
		snapInterval, e = time.ParseDuration(*snapFlag)
		if e != nil || snapInterval <= 0 {
			log.Fatalf("无效的 -snapshot-interval: %s", *snapFlag)
		}
	}

	root := *rootFlag
	if root == "" {
		root = detectRoot()
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	cfg := loadConfig(root, *envFlag, *addrFlag)
	staticDir := filepath.Join(root, "src", "frontend")
	dbPath := filepath.Join(root, "data", "finance.db")

	var err error
	db, err = openDB(dbPath)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer db.Close()
	startSnapshotLoop(db, dbPath, snapInterval) // 真实数据兜底：运行期间数据一变就留快照

	if _, err := os.Stat(staticDir); err != nil {
		log.Printf("警告：静态目录不存在 %s", staticDir)
	}

	ocr = NewOCRProvider(root, *pythonFlag)
	log.Printf("正在自检本地 OCR 引擎 ...")
	ocr.Check()
	if ocr.Available {
		log.Printf("OCR 引擎: %s（本地离线）", ocr.Engine)
	} else {
		log.Printf("OCR 不可用: %s", ocr.Hint)
	}

	mux := http.NewServeMux()
	registerAPI(mux)
	fs := http.FileServer(http.Dir(staticDir))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "接口不存在")
			return
		}
		// 记住"我要用电脑版"的选择：手机上点过一次后就不再自动跳极简页
		if d := r.URL.Query().Get("desktop"); d == "1" || d == "0" {
			http.SetCookie(w, &http.Cookie{
				Name:     desktopCookie,
				Value:    d,
				Path:     "/",
				MaxAge:   180 * 24 * 3600,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		// 手机访问默认进极简页（只上传账单）；带 ?desktop=1 或点过"电脑版"则强制电脑版
		if r.URL.Path == "/" && !wantsDesktop(r) && isMobileUA(r.UserAgent()) {
			// 相对跳转，兼容 Nginx 子路径 /finance/ 反代
			redirectRel(w, "mobile.html", http.StatusFound)
			return
		}
		fs.ServeHTTP(w, r)
	})

	handler := newAuthGate(cfg.AccessToken).wrap(mux)

	log.Printf("数据库: %s", dbPath)
	log.Printf("前端目录: %s", staticDir)
	log.Printf("监听地址: %s（来源: %s）", cfg.Addr, cfg.Source)
	if cfg.AccessToken == "" {
		log.Printf("访问口令: 未设置（无鉴权，只建议在可信内网使用）")
	} else {
		log.Printf("访问口令: 已启用（来源: %s）", cfg.TokenSource)
	}
	for _, u := range listenURLs(cfg) {
		log.Printf("可访问: %s", u)
	}
	// 固定入口：定期把当前隧道域名推给网关（没配 GATEWAY_URL 就什么都不做）
	if rep := newGatewayReporter(cfg); rep != nil {
		rep.start()
	}
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}

// isMobileUA 判断手机 / 平板浏览器。iPad 的 Safari UA 与桌面 Mac 相同，故不列入。
func isMobileUA(ua string) bool {
	l := strings.ToLower(ua)
	if l == "" {
		return false
	}
	for _, k := range []string{"mobile", "android", "iphone", "ipod", "windows phone", "micromessenger", "harmonyos"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// desktopCookie 记住用户在手机上选择了"电脑版"，避免每次都被跳回极简页。
const desktopCookie = "finance_desktop"

// wantsDesktop 判断是否要电脑版：URL 带 ?desktop=1，或之前点过"电脑版"留下的 Cookie。
// 显式的 ?desktop= 参数优先于 Cookie，否则"切回手机版"会被上一次存的 Cookie 盖掉。
func wantsDesktop(r *http.Request) bool {
	if d := r.URL.Query().Get("desktop"); d != "" {
		return d == "1"
	}
	if c, err := r.Cookie(desktopCookie); err == nil && c.Value == "1" {
		return true
	}
	return false
}

// redirectRel 用「原样写入」的相对 Location 跳转。
//
// 不能用 http.Redirect：它会把相对路径按后端看到的 r.URL.Path 拼成绝对路径，
// 而经过 Nginx 子路径反代（/finance/ → /）后后端看到的是 "/"，
// 拼出来的 "/mobile.html" 会丢掉 /finance 前缀、直接 404。
// 这里原样写 "mobile.html"，交给浏览器按真实地址（/finance/）解析。
func redirectRel(w http.ResponseWriter, loc string, code int) {
	w.Header().Set("Location", loc)
	w.WriteHeader(code)
}

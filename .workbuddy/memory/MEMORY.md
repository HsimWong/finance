# 项目长期笔记 · 家庭财务管理系统

## 定位

家庭 2 人自用记账平台。主链路：账单截图 → 本地 OCR 识别填入 → 复核生成收支流水，账单里的家庭用品自动进库存；分类树 + 标签双维度，跟踪支付宝小荷包收入资金预分配进度，支持成员 × 分类 × 标签 × 时间组合查询。
一期（记账/OCR/库存/临期提醒）已完成。二期规划：页面/APK 自动取账单、微信推送提醒、采购清单联动。
2026-11-13 起试用，库内为真实家庭数据（此前测试数据未清，发布时需按下方发布流程处理）。

## 强制约束（不要擅自改变）

- 后端 **无框架 Go**（标准库 net/http，可用库不引 Web 框架）；前端**原生 HTML+CSS+JS，无构建**；数据库 **本地 SQLite**，截图 BLOB 入库；敏捷增量开发。
- **改完 `src/frontend/js/*.js` 必须 `node --check`**（node v22 可用）。`go test` 抓不到 JS 语法错误。2026-11-18 因 `renderSettings()` 里 `const cats` 重复声明导致整个 app.js 不执行、全站白屏，用户误以为数据库丢了。排查用 Node 内置 WebSocket 连 Chrome `--remote-debugging-port` 走 CDP 收 `Runtime.exceptionThrown`；`--screenshot` 会把空页面拍下来误导判断。前端静态文件改动无需重编译/动库，强刷即生效。
- **动数据库前必须先备份并验证**：`python scripts/db_admin.py backup --label before-xxx` → `verify` → 才改结构 → 启动后 `info` 核对行数。绝不删库重来。
- **备份只保留最新一份**（用户 2026-11-18 明确），其余全删省空间。已在 `backupDB`（Go）与 `db_admin.py` 实现自动收敛（含孤立 -wal/-shm 清理、restore 保护来源）。变更过程中保留变更前快照，**任务收尾时**收敛到唯一一份。此规则覆盖旧的"startup 20 份/auto 48 份/daily 永久"约定。
- **所有 URL 必须相对路径**（`api/x` 而非 `/api/x`）：系统挂在阿里云 `/finance/` 与 WorkBuddy 网关下，绝对路径会 404。后端跳转用自写 `redirectRel`，不用 `http.Redirect`。
- **交叉编译二进制必须带 `-root`**：systemd 里显式 `-root /home/admin/finance`，否则读不到 `.env` → 鉴权静默关闭、DB 路径错。
- **库存同步必须显式**：`entryInput.SyncStock bool` 控制；不传/false 完全不碰库存，true+清单重建本流水自动项，true+`[]` 清空自动项。**禁止再用 nil/空数组/非空编码多种副作用**（用户 2026-11-19 严令，别拿"需求/架构"当挡箭牌，这是 bug）。手工库存 `auto_created=0` 任何路径都不动。
- **SQLite 单连接（`SetMaxOpenConns(1)`）下事务内禁止用全局 `db` 查询**：会永久死锁。品类规则读取有 `loadStockRulesFrom/findStockRuleByKeyFrom/matchStockRuleFrom/resolveStockCategoryFrom`，事务里一律传 `tx`。卡死时用 `go test -timeout 25s` 看 goroutine 栈定位。

## 已定技术决策（速览）

- Go 1.26；纯 Go 驱动 `modernc.org/sqlite`（无 CGO，本机无 gcc）；金额 `INTEGER` 存分，请求用元、响应给分+元串。默认 `:8081`，`-addr`/`-root` 可覆盖。
- **分类树 + 标签**：分类树主领域可嵌套（v2 一级 18 域，`汽车` 独立、`保险` 只人身/财产），标签跨领域切片（猫咪/房租/旅行）。`findCategoryID` 叶子优先消歧（否则「保险」命中一级）。树变动走 user_version 迁移 + 升级前备份，id 不变。
- **OCR**：RapidOCR+ONNX 本地离线（sidecar `scripts/ocr.py`，stdin 图片→stdout JSON），Go 侧 `OCRProvider` 可插拔、启动自检、失败降级粘贴文本。**识别结果不直接落库**：先填入表单、低置信标黄、明细可编辑，用户确认后一次事务写 entries+stock_items+stock_movements。
- **库存品类（v5 可维护）**：`stock_categories` 表落库，写死 `stockRules` 仅兜底/种子。`loadStockRules()` 运行时读表，自定义（`is_builtin=0`）优先；规则经 `/api/meta` 的 `stock_rules` 下发所有下拉。内置 6 类只在表空时种子。品类 key 稳定，改名同步 `stock_items.category_name`，有引用则拒绝删除。判定优先级 `resolveStockCategory`：显式有效 `category_key` 优先（勿用物品名覆盖）→ 名称匹配 → other → 兜底其他。管理 API `/api/stock-categories`，UI 在基础数据页。
- **计划单（v4）**：`plans` + `plan_allocations` 多对多关联预算，不动 allocations。clear = 标记 completed + 完成时间，不删任何行。流水挂预算时在同一事务内 clear 整张计划单（`clearPlansForAllocation`），去掉关联不回滚。可 reopen。
- **数据保护（ADR 0002）**：WAL 下禁止裸拷 db 当备份。`backup.go` 用 `VACUUM INTO` + `user_version` 版本化迁移，变更前强制备份、失败中止启动。schemaVersion 现为 **5**。
- **访问口令**：`auth.go` 的 authGate，Cookie(`finance_access` 30 天)/`?token=`/`X-Access-Token` 三种，常量时间比较，同 IP 10 分钟失败 10 次 429。口令来自 `.env` 的 `ACCESS_TOKEN`，空则关闭（仅可信内网）。公网必须开启。`isPublicStatic` 仅放行 `/manifest.json`、`/sw.js`、`/icons/*` 供 PWA 安装（浏览器拉 manifest 不带页面 Cookie，不放行会 401 装不上）；API/页面仍严格鉴权。
- **配置**：`config.go` 解析 `.env`（KEY=VALUE，export/引号），优先级 `-addr` > 环境变量 > .env > 默认。`ACCESS_TOKEN` 不入库，模板 `.env.example`。
- **手机端 PWA（2026-11-19）**：`mobile.html`+`js/mobile.js`+`css/mobile.css` 是 app 式布局（底部导航：记账/账单/我的），支持手动记账、拍照 OCR、按月账单列表与汇总、添加到主屏幕；`manifest.json`+`sw.js`+`icons/` 为 PWA 外壳，SW 只缓存 app shell 不缓存 API。共用 Cookie，`isMobileUA` 在 `GET /` 302 到手机页，`/?desktop=1` 强制电脑版，iPad 不判移动。手机端需能看账单，不再只是拍照页。
- **一键启动**：`bash scripts/start.sh`（--local/--stop/--restart），编译→服务→隧道→打印地址；端口口令读 .env 不写死。

## 公网三处部署

- **阿里云（生产）**：`47.94.236.198`，`/home/admin/finance`，systemd `finance.service` 监听 127.0.0.1:8081，Nginx `/finance/` → `127.0.0.1:8081/`（剥前缀）。发布：备份+验 integrity → 换 `bin/finance` → `systemctl restart finance` → 验六条路径。用户自己执行 git push/pull。
- **本机常驻**：计划任务 `FamilyFinance`（bash scripts/start.sh，AtLogOn+每 15 分钟，IgnoreNew 自愈）。**从工具里起的进程会被会话清理带走，只有计划任务能活**；`Stop-ScheduledTask` 不杀子进程，彻底停用用 `bash scripts/start.sh --stop`。`schtasks.exe` 被黑名单，只能用 `Get/Start/Stop-ScheduledTask`。`daemon.sh`/`supervisor.sh`/`finance-daemon` 是未启用方案，别照它操作。
- **固定入口网关**：独立项目 `C:\Users\WangXin\WorkBuddy\gateway`（纯标准库 Go，零依赖），部署在 WorkBuddy「发布为应用」沙箱。默认 `GW_MODE=redirect` 门户跳转（302 到当前隧道域名，带 no-store，**绝不能 301**）。服务进程 `reporter.go` 每 5s 扫 `logs/tunnel.log` 取最后域名变化即报，每 60s 无条件心跳（应对沙箱回收重启）。分享链接必须带 `?sharecode=`，`GATEWAY_URL` 要连 sharecode 一起存。发布目录 `gateway-deploy/`（纯源码），改完 `bash gateway/sync.sh`；`gateway/` 里 exe 被安全策略锁死。
- **隧道**：cloudflared quick tunnel 必须 `--protocol http2`（QUIC/UDP 7844 被阻断）。本机终端注入了 `HTTP_PROXY`，`start.sh` 已用 `env -u ...` 清掉并加 tunnel_reachable 健康检查。偶发死域名（边缘 530）或 DNS NXDOMAIN，杀掉重开换域名即可。
- **排查口诀**：固定入口 530=隧道域名死；404=网关注册表没该 prefix；本机 401 且隧道直连 401=正常。

## 本机环境坑

- `go run .` 被安全策略拒（Access denied），改 `go build -o scripts/bin/finance.exe` 后运行。
- 系统 `HTTP_PROXY=http://127.0.0.1:32014`，curl 测本机端口必须 `--noproxy '*'`，否则 502 假象。
- Git Bash 的 `/tmp` 与 Windows 原生程序 `/tmp` 不同路径，node/python 读临时文件用项目内相对路径。
- Git Bash `kill` 杀不掉 Windows 原生进程（cloudflared 留孤儿），用 `taskkill //F //PID`。
- `schtasks /tr` 走 GBK，中文/变音符路径会坏；托管入口放纯 ASCII 目录，非 ASCII 路径只在 .env 里。

## 目录约定

- `docs/01-需求与范围/原始构想.md` 保留用户原话，**不要改写**；`docs/README.md` 文档索引 + 待确认清单（用户会补想法）。
- `src/backend/` Go，`src/frontend/` 原生前端，`data/finance.db` 运行数据（不入库），`data/backups/`（只留最新一份）。

## 发布流程（当前库为测试数据，正式启用时执行）

1. 完成待发布功能 → `python scripts/db_admin.py backup --label before-release`（已 verify）
2. 停服务 → `python scripts/db_admin.py reset --yes --force-with-data`（清 8 张业务表，保留 members/categories/tags/stock_categories）
3. 启动核对业务表全 0、基础配置还在；**成员「成员 A/B」要改成真人姓名**，58 分类/17 标签/6 库存品类确认是否符合预期
4. 阿里云则交叉编译带 `-root /home/admin/finance`、scp、systemd 重启验路径。**清空数据这步必须等用户明确说"开始发布"才做**。

## 待确认（详见 docs/README.md）

Q2 分类树+标签是否认可；Q3 小荷包分配粒度与完成口径；Q4 面板收入资金进度口径；Q5 成员姓名/是否登录；**Q6 微信提醒渠道（新增账单推送订阅者——进行中）**；Q7 品类默认保质期；Q8 库存单位汇总口径；Q9 识别草稿是否持久化。

## 复用素材 / 其他项目

- `assets/fixtures/sample_bill.png`：中文模拟账单（永辉超市，5 项，65.40 元），验证 OCR 全链路。
- 法语学习平台（`C:\Users\WangXin\WorkBuddy\Français Prograssive`，端口 8080，french.db 150MB，go:embed）：托管目录 `french-daemon/`（纯 ASCII），计划任务 `FrenchService`/`FrenchTunnel`，网关 prefix=french。前端绝对路径，redirect 模式下无需改。启动约 60 秒（加载大库）。详情见 `french-daemon/README.md`。

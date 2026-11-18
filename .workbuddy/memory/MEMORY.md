# 项目长期笔记 · 家庭财务管理系统

## 定位

家庭 2 人自用的定制化记账平台。核心场景：上传账单截图 → 本地 OCR 自动识别并填入 → 复核后生成收支流水，账单里的家庭用品自动进入库存；细分账单所属领域，跟踪支付宝小荷包的收入资金预先分配进度，支持任意维度组合查询（成员 × 分类 × 标签 × 时间）。
一期已完成：记账主链路、OCR 识别、库存管理与页面内临期通报。
二期：页面/APK 自动获取账单、房租保险的微信推送提醒、囤货与采购清单联动。
**2026-11-13 18:20 起进入试用**，演示数据已清空，库里将写入真实家庭财务数据。

## 用户明确的技术约束（不要擅自改变）

- 后端：**无框架 Go**，允许用库，不引 Web 框架。
- 前端：**不使用框架**，纯 HTML + CSS + JavaScript，无构建步骤。
- 数据库：**本地 SQLite**；截图保存在服务端且图片入库。
- 开发方式：**敏捷**，先把已确定的功能搭出可运行增量，再逐步补。
- **【强制】试用期间任何需要动数据库的新需求，必须先保存好真实数据、确认可恢复后再改。** 固定流程：`python scripts/db_admin.py backup --label before-xxx` → `verify` → 才动代码/结构 → 启动后 `info` 核对行数。绝不允许直接删库重来。
- **【强制 · 用户 2026-11-18 明确】备份只保留最新一份，其余全部删除以节省存储**（"always keep the latest backup of the database, and abort others to save storage"）。每次产生新备份/回档/清理后，最后都要把 `data/backups/` 收敛到只剩**最新且已 verify 的那一份**（连同它的 `.json` 元数据；清掉孤立的 `-wal`/`-shm` 残留）。注意：变更*过程中*仍保留变更前快照当安全网，**任务收尾时**再执行收敛。此规则覆盖原「手动与变更前备份永不自动清理」的旧约定。

## 已定技术决策

- Go 1.26 标准库 `net/http`；本机无 gcc，SQLite 驱动用纯 Go `modernc.org/sqlite`（无 CGO）。
- 金额一律 `INTEGER` 存「分」；请求用元、响应给分 + 元字符串。
- 默认端口 `:8081`（用户另有一个 localhost:8080 的法语学习平台）；`-addr` / `-root` 可覆盖。
- **分类树 + 标签双维度**：分类树是主归类领域（可嵌套），标签是跨领域对象切片（猫咪、房租、旅行）。这是同时回答「成员外卖花了多少」「全家交通」「猫咪花了多少」的关键。
- **分类树 v2（ADR 0003）**：一级领域 18 个，`汽车` 独立成域（`加油 · 停车费 · 保养 · 维修 · 保险`）；`交通` 只剩公交地铁/打车，`保险` 只剩人身险/财产险。升级时只改 `parent_id`/改名，**id 不变**，历史流水引用不受影响。分类树任何变动都走 `user_version` 迁移 + 升级前强制备份。
- **同名分类消歧**：`parse.go` 的 `findCategoryID` 按「叶子分类优先」匹配，否则「保险」会命中一级分类而非 `汽车›保险`。
- 图片存 `screenshots.data` BLOB，读取统一走 `/api/screenshots/{id}/image`，保证以后可平切到磁盘文件。
- 预算 `allocations` 按 `period`(YYYY-MM) + `batch`(first 月初 / mid 月中)，流水挂 `allocation_id` 算进度；`category_id IS NULL` 即未分类，进报警表。
- **OCR**：RapidOCR + ONNX Runtime 本地离线（Python sidecar `scripts/ocr.py`，stdin 图片字节 / stdout JSON），Go 侧 `OCRProvider` 可插拔、启动自检，失败降级到粘贴文本解析。Go 后端仍无框架，不引 Python 运行时为 Go 依赖。
- **识别结果不直接落库**：先自动填入表单、低置信标黄、商品明细可编辑，用户确认后一次事务写 `entries` + `stock_items` + `stock_movements`。这是刻意的人机分工，不要改成识别即入库。
- **库存品类规则**：`parse.go` 的 `stockRules`，顺序即优先级；调料必须排在粮油前（否则「酱油」被单字「油」误匹配）。规则经 `/api/meta` 的 `stock_rules` 下发前端，避免两套清单。
- **品类判定优先级（ADR 0004，v3 修复）**：`resolveStockCategory` — ① 有有效 `category_key` 就以它为准、名称从规则表反查（**不要再拿物品名覆盖用户的显式选择**，这是「别嘌醇缓释胶囊选了药品却显示其他」的根因）；② 未给品类才按名称匹配；③ 显式 `other` 就尊重；④ 都推不出落「其他」。药品除常见药名外靠 `medicineForms` 剂型词兜底（胶囊/缓释/肠溶/分散片…），并用 `medicineFormExclusions` 排除咖啡胶囊、薯片、麦片等。
- 库存项 `auto_created=1` 表示账单自动生成；编辑/删除流水只处理这类，手工录入项不动。`PUT /api/stock/items/{id}` 支持改 `category_key`（编辑弹窗已带品类下拉）。
- **数据保护（ADR 0002）**：WAL 模式下**禁止裸拷贝 `finance.db` 当备份**（实测主库 4 KB / WAL 700 KB）。`scripts/db_admin.py` 用 SQLite 在线备份 API；`src/backend/backup.go` 用 `VACUUM INTO` + `PRAGMA user_version` 版本化迁移，变更前强制备份、失败即中止启动；每次启动留快照（滚动 20 份）；运行时每 30 分钟按数据指纹自动快照（`-snapshot-interval` 可调，每日首份 `daily-*` 永久保留、`auto` 留 48 份）。`reset` 只清 6 张业务表，保留 members/categories/tags，库内有业务数据时须加 `--force-with-data`。回档会删旧 WAL/SHM。备份与库同盘，需定期外拷。回归测试见 `src/backend/backup_test.go`。
- **监听地址可配置**：根目录 `.env`（`BIND_HOST` / `BIND_PORT`，可选 `HTTP_ADDR`），优先级 `-addr` > 环境变量 > `.env` > 默认 `0.0.0.0:8081`；解析在 `src/backend/config.go`，无第三方配置库。绑定 `0.0.0.0` 时启动日志列出局域网地址（手机同 WiFi 可上传截图）。`.env` 不入库，模板 `.env.example`。
- **访问口令**：`src/backend/auth.go` 的 `authGate`，支持 Cookie(`finance_access`，30 天) / `?token=` / `X-Access-Token` 三种方式，常量时间比较，同一 IP 10 分钟内失败 10 次即 429；未授权 API 返回 401 JSON、页面返回登录页。口令来自 `.env` 的 `ACCESS_TOKEN`，为空则关闭（仅可信内网）。公网暴露时必须开启。
- **手机端极简页**：`src/frontend/mobile.html` + `js/mobile.js` + `css/mobile.css`，只做「拍照/选图 → OCR → 核对金额分类 → 保存」，与主站共用 Cookie。`main.go` 的 `isMobileUA` 在 `GET /` 上 302 到 `/mobile.html`，`/?desktop=1` 强制电脑版；iPad 不判为移动（UA 与 Mac 相同）。
- **公网访问**：`scripts/tunnel.sh` 启动 cloudflared quick tunnel，**必须 `--protocol http2`**（本机 QUIC/UDP 7844 被阻断，用 quic 会一直重试并返回 530）。quick tunnel 每次重启换 `*.trycloudflare.com` 地址、无可用性保证，长期应改 named tunnel + 自有域名 + Cloudflare Access。**偶发「分了域名但 DNS 没建起来」（curl 000 / nslookup NXDOMAIN）**，等也没用，杀掉 cloudflared 重开换域名即可。**另一坑：终端注入了 `HTTP_PROXY/HTTPS_PROXY`（如沙箱 127.0.0.1:32014）时 cloudflared 会继承代理，代理不放行 7844 就报 `dial tcp <edge>:7844: i/o timeout`、随后域名被回收**；`start.sh` 已用 `env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy` 清掉，并加了 `tunnel_reachable` 健康检查（PID 在≠隧道活）。
- **一键启动**：`bash scripts/start.sh`（按需编译 → 起服务 → 建隧道 → 打印本机/局域网/公网地址与口令；`--local` 只起服务、`--stop` 停服务与隧道、`--restart` 先停再起），pid 与日志在 `scripts/bin/*.pid`、`logs/`。端口与口令全读 `.env`，不要写死在脚本里。

## 本机环境坑（会反复遇到）

- `go run .` 会被安全策略拒绝（`fork/exec ... Access is denied`），改用 `go build -o scripts/bin/finance.exe` 后直接运行。
- 系统设置了 `HTTP_PROXY=http://127.0.0.1:32014`，用 curl 测本机端口必须加 `--noproxy '*'`，否则代理连不上会返回 502「upstream connect failed」，看起来像服务挂了。
- Git Bash 的 `/tmp` 与 Windows 原生程序的 `/tmp` 不是同一路径；node/python 读临时文件时用项目内相对路径更稳。
- 单文件静态服务器：`GET /` 已在 Go 里按 UA 重定向，静态文件由 FileServer 直接提供，新增页面放 `src/frontend/` 即可访问。

## 目录约定

- `docs/01-需求与范围/原始构想.md` 保留用户原话，**不要改写**。
- `docs/README.md` 是文档索引 + 待确认问题清单（用户也会在这里随手补想法）。
- `src/backend/` Go 服务，`src/frontend/` 原生前端，`data/finance.db` 运行期数据（不入库版本库），`data/backups/` 备份（**只留最新一份，见上方强制规则**）。

## 固定入口网关（2026-11-13 上线）

- 目标：给 quick tunnel 不断变化的域名一个**固定入口**。独立项目 `C:\Users\WangXin\WorkBuddy\gateway`，**纯标准库 Go、零依赖、go 1.21**，部署在 WorkBuddy「发布为应用」沙箱。
- **用反向代理，不要 302 跳转**：跳转会暴露随机域名、让 Cookie 跨源失效、坏 PWA。
- 注册表用 JSON 文件而非数据库：它是**可重建缓存**（程序每次启动 + 每 30s 上报，重启自愈），且零依赖不会卡沙箱的 `go mod`。
- 四个必须处理的点：① 被代理程序前端必须用相对路径（`api/x` 不是 `/api/x`），否则全 404；② `GET /finance` 必须 301 到 `/finance/`；③ 重写 `Location`；④ 清 Cookie `Domain` 并把 `Path` 收拢到 `/{prefix}`。代理**不走 `HTTP_PROXY`**。
- 上报**由服务进程自己负责**：`src/backend/reporter.go`。**快慢双周期**——域名变化每 5s 扫 `logs/tunnel.log`（取**最后一个** `*.trycloudflare.com`）变了立刻 POST `/api/register`；心跳每 `GATEWAY_INTERVAL`（默认 60s）无条件重报，负责网关沙箱重启后把注册表长回来。失败退避 30s。原因：脚本的前台循环一退出就断供，且关不掉「关终端就死」。`start.sh` 只保留启动时的一次性上报。配置为 `.env` 的 `GATEWAY_URL/TOKEN/PREFIX/NAME`（可选 `GATEWAY_INTERVAL`、`GATEWAY_TUNNEL_LOG`）。日志策略：域名变化打一行、同样错误只打一次、心跳成功每 30 分钟留痕。
- **网关默认是「门户跳转」模式**（`GW_MODE=redirect`，2026-11-14 用户拍板）：首页是卡片门牌，卡片与 `/finance`(/) 都 **302** 到当前隧道域名并带 `no-store`，跳转时剔掉 `sharecode`/`token`。地址栏会变成 `xxx.trycloudflare.com`，之后直连隧道不再经网关——因此网关沙箱回收、注册表清空都不影响已打开的页面。**绝不能用 301**（浏览器长期缓存会把用户钉死在死链上）。`GW_MODE=proxy` 可切回旧的反代模式（前缀/`Location`/`Set-Cookie` 改写代码仍在）。
- **进程常驻最终由计划任务 `FamilyFinance` 解决（2026-11-14 建，11-16 补自愈）**：从工具里 `( 程序 & )` 起的进程会被会话清理带走（`Fs3aOI` 跑满 4h12m 后仍被杀，父子一起没）；`DETACHED_PROCESS` 也逃不出沙箱；`Start-Process` 起 bash 被安全策略拦。**只有计划任务托管能活**。
  - 实际生效任务：`FamilyFinance` —— 动作 `C:\Program Files\Git\bin\bash.exe scripts/start.sh`，工作目录项目根；触发器 = `AtLogOn` + `Once`（`RepetitionInterval=PT15M`，Duration 留空=无限）；`MultipleInstances=IgnoreNew`、`ExecutionTimeLimit=PT0S`、`RestartCount=3 / 1 分钟`。
  - 自愈原理：服务活着 → `start.sh` 走 `while svc_up` 驻留 → 任务 Running → 15 分钟后触发被 IgnoreNew 跳过；服务挂了 → bash 退出 → 任务 Ready → 下次触发（≤15 分钟）自动拉起。
  - **`scripts/daemon.sh` + `supervisor.sh` + `finance-daemon/{service,tunnel}.cmd` 是另一套从未启用的方案**（`FinanceService`/`FinanceTunnel` 任务没注册成功，`schtasks.exe` 被安全策略拉黑）。`start.sh` 第 48 行靠 `schtasks //query` 判断是否转交，永远为假，所以两套不会打架——但别再照那套操作。
  - 停止：`Stop-ScheduledTask` **不会**杀掉已跑的子进程（实测留下孤儿）；彻底停用 `bash scripts/start.sh --stop`（按 pid 文件 + 端口反查）。
  - `schtasks.exe` 在黑名单，查询/操作任务只能用 `Get-ScheduledTask` / `Start-ScheduledTask` / `Stop-ScheduledTask`。
  - **新建隧道约有一半概率拿到「注册成功但边缘一直 530」的死域名**（`--resolve` 指定边缘 IP 仍 530，可排除本地 DNS），只能杀掉重开换域名。
- **排查口诀**：固定入口 530 = 网关指的隧道域名已死（通常是隧道刚重启换域名、注册表还没更新，现在 ≤2s 自愈）；固定入口 404 = 网关注册表里没这个 prefix（服务没起来或上报被拒）；本机 401 且隧道域名直连 401 = 一切正常。
- WorkBuddy 分享链接**必须带 `?sharecode=`**，首次访问种 `__wb_rsc` Cookie（30 天）后子请求自动带上，所以 `GATEWAY_URL` 要把 sharecode 一起存。
- 发布目录用 `gateway-deploy/`（纯源码），改完执行 `bash gateway/sync.sh` 同步——`gateway/` 里的 exe 会被安全策略锁死导致打包失败。
- **沙箱会被回收重启（已实测 uptime 归零、注册表清空），但链接不变、请求仍 200，属按需唤醒**。因此服务进程每 60 秒**无条件心跳重报**，让注册表在沙箱重启后自愈（代价是最长约 60 秒空窗）。

## 法语学习平台接入网关（2026-11-14 02:00）

- 项目在 `C:\Users\WangXin\WorkBuddy\Français Prograssive`（`francais-progressive`，端口 8080，SQLite `french.db` 150 MB，web 与词库全部 `go:embed`）。用户口中的「laowang 项目」即此。
- **托管目录 `C:\Users\WangXin\WorkBuddy\french-daemon`**（纯 ASCII 路径，原因见坑 3）：`supervisor.sh service|tunnel`、`.env`（`APP_ROOT/APP_PORT/DB_PATH/GATEWAY_*`）、`bin/francais.exe`（编译副本）、`README.md` 写清全部运维命令。
- 计划任务 `FrenchService` / `FrenchTunnel`，入口 `french-daemon/{service,tunnel}.cmd`；开机自启靠启动文件夹 `french-daemon.cmd`。
- 网关注册 `prefix=french`、`name=法语学习平台`，`GW_MODE=redirect` → `/french` 302 到隧道域名。**法语前端是绝对路径**（`/css/style.css`），跳转后落在隧道根路径正好可用，**不需要改前端**（这是 redirect 模式相对 proxy 模式的实际收益）。
- **三个新坑（已写进 `french-daemon/README.md`）**：
  1. **本机 DNS 不可靠**：本地 DNS（10.2.16.100）对新 `*.trycloudflare.com` 返回 NXDOMAIN（负缓存），而 8.8.8.8 正常 → 曾连续误杀 4 个可用域名。判定改成问 8.8.8.8（除 8.8.8.8 外解析出 IP 即算发布）。
  2. **Git Bash `kill` 杀不掉 Windows 原生进程**：每换域名留一个孤儿 cloudflared，累积到 8 个。必须 `taskkill //F //PID`。
  3. **`schtasks /tr` 走 GBK**：中文/带变音符的路径会被弄坏，入口与 `bin/` 必须放纯 ASCII 目录，非 ASCII 路径只出现在 `.env` 里由 bash 读。
- `francais.exe` 启动要 **约 60 秒**（加载 150 MB 库 + seed 词库），期间 8080 拒连，属正常。
- 实测自愈：杀 cloudflared → 25 秒换新域名并上报；杀 francais.exe → 60 秒恢复；网关探活两个应用均 `healthy=True`。

## 待确认（会影响下一步）

Q1 已确认并实现（OCR 自动填入）。仍待确认：Q2 分类树+标签是否认可；Q3 小荷包分配粒度与「完成情况」口径；Q4 面板收入资金进度口径；Q5 成员姓名与是否要登录；Q6 微信提醒渠道（囤货与财务流水打通已实现）；Q7 各品类默认保质期天数；Q8 库存单位汇总口径；Q9 识别草稿是否持久化。
详见 `docs/README.md`。

## 复用素材

`assets/fixtures/sample_bill.png` 是中文模拟账单（永辉超市、5 项家庭用品、合计 65.40 元），用于验证 OCR → 填表 → 自动入库全链路。
`data/backups/20261113-181627_before-trial-reset.db` 与 `20261113-181957_before-reset.db` 是清空前的完整演示库（5 条流水 / 2 张截图 / 5 项库存），可用于验证回档链路，**不是真实数据**。

## 公网部署到阿里云后的两个硬约束（2026-11-17）

- **服务器**：`47.94.236.198`，代码在 `/home/admin/finance`，systemd 单元 `finance.service`，监听 `127.0.0.1:8081`，Nginx `location /finance/ → 127.0.0.1:8081/`（**会剥掉 /finance 前缀**，后端看到的永远是 `/`）。
- **约束一：前端与后端所有 URL 必须是相对路径。** 任何 `action="/x"`、`href="/x"`、`http.Redirect(w,r,"/x")` 在 `/finance/` 下都会 404（`http.Redirect` 还会把相对路径按后端路径拼成绝对路径，必须改用自写的 `redirectRel`）。
- **约束二：交叉编译的二进制必须带 `-root`。** `detectRoot()` 已改为优先按可执行文件位置推断，但 systemd 里仍显式写 `-root /home/admin/finance`；否则会去找编译机路径，导致 `.env` 读不到、**鉴权静默关闭**、DB 指向错误路径。
- 部署顺序：先 `python3` sqlite3 backup API 备份 `data/finance.db` 并验 `integrity_check`，再换 `bin/finance`、`systemctl restart finance`，最后经 `https://127.0.0.1/finance/` 验六条路径（未登录/登录/手机/电脑版/切换/接口）。

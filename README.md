# 家庭财务管理系统

> 阶段：**敏捷增量 2 完成，试用中**（OCR 自动识别 + 家庭用品自动入库已可运行）
> 核心场景：上传账单截图 → 本地 OCR 自动识别并填入 → 人工复核确认 → 生成收支流水，账单里的家庭用品同时自动进入库存；按细分领域记账，并跟踪收入资金的预分配进度。

## 快速启动

**一次性安装本地 OCR 依赖**（纯离线，无需密钥）：

```bash
python -m pip install rapidocr_onnxruntime
```

启动服务（推荐，一条命令搞定「按需编译 → 起服务 → 建 Cloudflare 隧道」）：

```bash
bash scripts/start.sh
```

脚本会打印本机 / 局域网 / 公网地址与访问口令；`Ctrl+C` 同时停掉服务与隧道。

| 命令 | 作用 |
| --- | --- |
| `bash scripts/start.sh` | 编译（源码有变化才编）→ 启动服务 → 建隧道 → 打印地址与口令 |
| `bash scripts/start.sh --local` | 只启动服务，不建隧道（仅局域网） |
| `bash scripts/start.sh --restart` | 先停旧进程再启动 |
| `bash scripts/start.sh --stop` | 停止服务与隧道 |

### 长期常驻（推荐）：Windows 计划任务

`start.sh` 是前台驻留脚本，**在终端/会话里跑就会被那个会话的生命周期绑住**：终端一关、
后台任务被清理，`finance.exe` 与 `cloudflared` 会跟着一起消失（本机已经踩过两次）。
要长期可用，交给 Windows 计划任务托管：

```powershell
# 已注册的任务名：FamilyFinance
#   触发器：登录时 + 每 15 分钟自检一次（服务挂了最多 15 分钟自动拉回）
#   设置：不限运行时长、崩溃后 1 分钟重试 3 次、不重复启动实例
Start-ScheduledTask -TaskName "FamilyFinance"    # 立即启动（服务已在跑则什么都不做）
Get-ScheduledTask -TaskName "FamilyFinance"      # 看状态：Running 正常，Ready 表示没在跑
Stop-ScheduledTask  -TaskName "FamilyFinance"    # 停止托管（注意：不会杀掉已跑的子进程）
Unregister-ScheduledTask -TaskName "FamilyFinance" -Confirm:$false   # 不用了才删
```

彻底停止服务用 `bash scripts/start.sh --stop`（按 pid 文件与端口反查杀干净）；只 `Stop-ScheduledTask`
会留下孤儿进程。查询/操作计划任务的 `schtasks.exe` 在本机被安全策略拦截，请用上面的 PowerShell
命令或「任务计划程序」图形界面。

> `scripts/daemon.sh` + `supervisor.sh` + `finance-daemon/*.cmd` 是另一套**未启用**的分体守护方案
> （依赖 `schtasks` 注册，而它在本机被拉黑），不要和 `FamilyFinance` 混着用。

也可以手动起（不开隧道）：

```bash
cd src/backend
go run .
```

若本机报 `fork/exec ... Access is denied`（安全软件拦截 Go 的临时可执行文件），改用固定路径编译后运行：

```bash
go -C src/backend build -o "$(pwd)/scripts/bin/finance.exe" .
./scripts/bin/finance.exe
```

打开 `http://localhost:8081`。启动时自动读取项目根目录的 `.env`，绑定 IP 与端口在 `.env` 里配置（见下文「监听地址配置」）；项目根目录可用 `-root`、配置文件可用 `-env`、Python 解释器可用 `-ocr-python` 指定。
首次启动自动创建 `data/finance.db` 并写入成员、分类树、标签与库存品类种子数据；同时自检 OCR 引擎、生成启动快照。
本机需 Go 1.22+ 与 Python 3；无 gcc 亦可（使用纯 Go SQLite 驱动）。

**没装 OCR 依赖也能用**：页面会提示引擎未就绪，可把账单文字粘贴进「识别不到？粘贴账单文字」框，系统用同一套规则解析并自动填表、自动入库。

### 监听地址配置（.env）

根目录提供 `.env`（实际配置，不入库）与 `.env.example`（模板）：

```ini
# 0.0.0.0 允许手机等局域网设备访问；127.0.0.1 仅本机
BIND_HOST=0.0.0.0
# 端口冲突时改这里
BIND_PORT=8081
# 可选：直接指定完整地址，优先级最高
# HTTP_ADDR=127.0.0.1:8081
```

优先级：**命令行 `-addr` > 环境变量 > `.env` > 默认值 `0.0.0.0:8081`**。
启动日志会打印「监听地址（来源）」与全部可访问地址，绑定 `0.0.0.0` 时会列出局域网 IP —— 手机连同一 WiFi 直接打开即可上传账单截图。

```bash
go run . -addr 127.0.0.1:9000        # 临时覆盖，仅本机 9000 端口
BIND_PORT=9000 go run .              # 环境变量覆盖 .env
go run . -env D:/config/finance.env  # 指定其它配置文件
```

端口或 IP 非法时服务会直接给出中文错误并退出，不会带着错误配置启动。

### 公网访问（Cloudflare 隧道）

```bash
bash scripts/start.sh          # 服务 + 隧道一起起，直接打印公网地址
```

只建隧道（服务已单独在跑时）：

```bash
bash scripts/tunnel.sh
```

等价于（本机 QUIC/UDP 7844 被网络阻断，**必须指定 HTTP/2**，否则 Cloudflare 边缘返回 530）：

```bash
cloudflared tunnel --url http://127.0.0.1:8081 --protocol http2 --no-autoupdate
```

日志里 `https://xxx-xxx-xxx.trycloudflare.com` 就是公网地址，手机用移动网络也能打开。

- **前提是设了口令**：公网地址任何人可访问，`.env` 的 `ACCESS_TOKEN` 是唯一门槛，首次输入后 30 天免输。`scripts/tunnel.sh` 检测到没设口令会警告。
- **临时域名**：quick tunnel 每次重启都换地址，无可用性保证。长期使用应建 named tunnel + 自有域名（需 Cloudflare 账号与域名），再配 Cloudflare Access 做邮箱/Google 登录。
- **流量经 Cloudflare 边缘**：账单截图会经过第三方，回源段是本机 HTTP。敏感数据需自行权衡。
- **停止**：结束 cloudflared 进程即可，地址立即失效（`bash scripts/start.sh --stop`）。
- **地址打不开（域名不存在 / NXDOMAIN）**：quick tunnel 偶尔会出现「已经分配了域名、但 Cloudflare 侧 DNS 没建起来」，表现为 `curl` 返回 000、`nslookup` 报 Non-existent domain，等几分钟也不会好。直接重开隧道换一个域名即可（`bash scripts/start.sh --stop` 后重新 `start.sh`）。
- **换口令**：改 `.env` 的 `ACCESS_TOKEN` 后重启服务，所有设备需重新输入。

### 固定入口（推荐给手机用）

临时域名每次重启都换，手机上的书签就废了。`.env` 里配好 `GATEWAY_URL` / `GATEWAY_TOKEN` /
`GATEWAY_PREFIX` 后，服务会**每 60 秒把当前隧道域名上报给固定入口网关**，你只需要记一个地址：

```
https://wbapp-zkmvjjcjly9s7dgylmnivh.app.workbuddy.host/?sharecode=QPFVA7KHN2
```

隧道换域名后，日志会出现 `固定入口已指向 https://xxx.trycloudflare.com`，手机上刷新固定入口
即可，不用改书签。上报失败只影响这个入口（日志 `固定入口上报失败: ...`），本机与局域网不受影响；
网关沙箱被回收时会话也会自己长回来（服务一直心跳）。

### 手机端极简页

手机浏览器访问同一地址会自动跳转 `/mobile.html`：只有「拍照 / 选图 → 自动识别 → 核对金额与分类 → 保存」四步，不显示流水、预算、统计、设置。识别出的家庭用品仍会随流水自动入库。

- 电脑上看手机版：`/mobile.html`
- 手机上回电脑版：`/?desktop=1`
- 口令会话与电脑版共用（同一个 Cookie）

### 试用状态

**2026-11-13 18:20 已清空全部演示数据**：流水、截图、预算、库存、出入库记录均为 0，从干净状态开始录入真实数据。`members`（成员 A / B）、`categories`（58 个分类）、`tags`（8 个标签）属于基础配置，予以保留，可在「设置」页改名增删。

**2026-11-13 19:00 分类树升到 v2**：新增「汽车」领域（加油 / 停车费 / 保养 / 维修 / 保险），原来挂在「交通」下的加油、停车费与「保险」下的车险已迁入汽车（改 `parent_id` 与名称，**id 不变，历史流水引用不受影响**）。升级前自动备份：`data/backups/*_before-migration-v2.db`。详见 `docs/99-决策记录/0003-汽车消费分类.md`。

清空前的完整演示库（5 条流水、2 张截图、5 项库存）保存在 `data/backups/20261113-181627_before-trial-reset.db` 与 `20261113-181957_before-reset.db`，随时可回档。

**2026-11-13 19:58 库存品类修复（v3）**：修正「选了药品却显示成其他」的问题——显式选择的品类现在优先于按名称推断，药品识别新增剂型词兜底（胶囊 / 缓释 / 肠溶 / 分散片 …），并把库里 `key=medicine / name=其他` 的历史记录按 key 反查纠正。库存编辑弹窗补了品类下拉。升级前自动备份：`data/backups/*_before-migration-v3.db`。详见 `docs/99-决策记录/0004-库存品类判定.md`。

`assets/fixtures/sample_bill.png` 是中文模拟账单，可用它先跑一遍「识别 → 填表 → 生成流水 → 自动入库」熟悉流程（试跑后删掉那条流水即可）。

## 数据安全约定（试用期间强制）

> 用户明确要求：**试用期间任何需要动数据库的新需求，必须先保存好真实数据，确认可恢复后再改。**

**关键陷阱**：数据库以 WAL 模式运行，数据常驻 `finance.db-wal`，主文件可能只有 4 KB（本次实测主文件 4 096 B、WAL 700 KB）。**直接复制 `finance.db` 当备份会丢数据** —— 一律使用下面的工具或 SQLite 在线备份 API。

四层保护（均已生效并实测，含自动化测试）：

1. **结构变更前强制备份**：Go 服务用 `PRAGMA user_version` 管理结构版本。任何迁移（`src/backend/backup.go` 里递增 `schemaVersion` 并在 `migrations` 登记语句）在动 DDL **之前**自动 `VACUUM INTO` 生成快照；备份失败直接中止启动，绝不带着真实数据冒险。
2. **启动快照**：每次启动留一份完整快照（30 分钟内重启不重复），滚动保留最近 20 份。手动备份、变更前备份、清空前备份**永不自动清理**。
3. **运行时定时快照**：服务一直开着也会每 30 分钟检查一次数据指纹，**只要数据有变化就自动备份**（`-snapshot-interval` 可调，`0` 关闭）。每天第一份标 `daily-YYYYMMDD` 永久保留，其余 `auto` 滚动保留 48 份 —— 专门覆盖「连续开几天、中途误删」的情况。
4. **统一管理工具** `scripts/db_admin.py`：备份 / 校验 / 回档 / 清空 / 查看，基于 SQLite 在线备份 API（含 WAL 已提交数据），每份备份附 `.json` 元数据（行数、哈希、完整性、结构版本）。`reset` 在库里有业务数据时必须额外加 `--force-with-data`，避免误清真实数据。

常用命令（清空 / 回档前请先停服务，工具会拦截服务仍在运行的情况）：

```bash
python scripts/db_admin.py info                        # 各表行数、WAL 状态、完整性、结构版本
python scripts/db_admin.py list                       # 查看所有备份
python scripts/db_admin.py backup --label 试用第1周     # 手动备份（永久保留）
python scripts/db_admin.py verify data/backups/xxx.db  # 校验哈希与完整性
python scripts/db_admin.py restore data/backups/xxx.db --yes  # 先备份现状，再回档
python scripts/db_admin.py reset --yes                # 先备份现状，再清空业务数据
```

**今后改数据库的固定流程**：`backup --label before-需求名` → `verify` 确认备份可用 → 才动代码 / 结构 → 启动后 `info` 核对行数。回档会自动删除旧 WAL/SHM 文件，避免旧日志覆盖恢复结果。

设计依据与实测记录见 `docs/99-决策记录/0002-数据保护策略.md`。

> ⚠️ 备份与数据库在同一块磁盘，只能防误操作、防迁移事故，**不能防磁盘损坏**。建议每周把 `data/backups/` 复制一份到移动硬盘或网盘。

## 目录说明

| 目录 | 用途 |
| --- | --- |
| `docs/` | 设计文档区，入口见 `docs/README.md`（含待确认问题清单） |
| `docs/01-需求与范围/` | 原始构想存档、范围与分期、验收口径 |
| `docs/02-功能设计/` | 功能清单、OCR 识别与库存联动设计 |
| `docs/03-技术方案/` | 技术选型与架构、接口清单 |
| `docs/04-数据模型/` | 表结构与查询口径 |
| `docs/99-决策记录/` | 关键取舍 ADR（含数据保护策略） |
| `docs/参考资料/` | 截图样例、竞品与调研资料 |
| `.env` / `.env.example` | 监听 IP 与端口配置（实际配置 / 模板），`.env` 不入库 |
| `src/backend/` | Go 服务（标准库 + modernc.org/sqlite），含 `backup.go` 备份与版本迁移、`config.go` 配置解析 |
| `src/frontend/` | 原生 HTML / CSS / JS，零构建；`mobile.html` 为手机端极简页 |
| `src/shared/` | 前后端共享约定（金额单位、枚举值） |
| `assets/screenshots/` | 截图样本（验证识别效果） |
| `assets/fixtures/` | 测试数据，含中文模拟账单 `sample_bill.png` |
| `data/` | SQLite 数据文件（不纳入版本库） |
| `data/backups/` | 自动快照与手动备份，附 `.json` 元数据 |
| `scripts/ocr.py` | 本地 OCR sidecar（stdin 收图片 / stdout 回 JSON），可整体替换 |
| `scripts/db_admin.py` | 数据库备份 / 校验 / 回档 / 清空工具 |
| `scripts/tunnel.sh` | 一键启动 Cloudflare 隧道（固定 HTTP/2），把服务临时暴露到公网 |
| `scripts/bin/finance.exe` | 编译产物（`go run` 在本机被安全策略拦截时用它启动），不入库 |
| `tests/` | 测试用例 |

## 设计要点

- **双维度归类**：分类树（餐饮 › 外卖、交通、居住 › 房租）作主归类，标签（猫咪、旅行、报销）作横向切片，两者可自由组合查询。
- **小荷包预分配**：每月月初 / 月中两批预算，流水挂到预算上，实时算已花 / 剩余 / 超额。
- **未分类报警**：分类留空的流水进报警表，行内归类后自动消失。
- **OCR 自动识别**：RapidOCR + ONNX Runtime 本地离线推理，Go 后端只依赖「图片字节 → 文本行」契约；纯 Go 规则解析出金额、时间、收支方向、商户、分类与商品明细，自动填入表单，低置信字段标黄，人工复核后才落库。
- **家庭用品自动入库**：账单中的瓜果蔬菜 / 肉蛋奶 / 粮油 / 调料 / 猫罐头 / 药品按品类自动进入库存，与流水在**同一事务**写入；默认保质期按品类预填，可逐行修改。库存页支持每日消耗、损耗、手工入库与临期/过期通报。
- **图片入库**：截图原始字节存 SQLite BLOB，单文件备份、事务一致；读取统一走 `/api/screenshots/{id}/image`，将来可平切换到磁盘存储。
- **金额以「分」为整数存储**，避免浮点误差。

## 约定

- 后端只用 Go 标准库 + SQLite 驱动，不引 Web 框架；前端无框架、无构建步骤。
- 文档统一 Markdown；`docs/01-需求与范围/原始构想.md` 保留你的原始文字，不改写。
- 空目录中的 `.gitkeep` 仅用于保留目录结构。
- **任何数据库结构或批量数据变更，先备份、验证可恢复，再动手**（见上文）。

## 进度

- [x] 建立目录骨架
- [x] 需求与范围、功能设计
- [x] 技术方案与数据模型
- [x] 增量 1：截图上传、收支流水、分类树/标签、多维查询、预算进度、总览面板、未分类报警
- [x] 增量 2：本地 OCR 自动识别与填表、粘贴文本兜底、家庭用品自动入库、库存管理与临期通报
- [x] 端到端验证通过（2026-11-13，真实中文账单截图全链路）
- [x] 数据保护基线：WAL 安全备份 / 回档 / 结构变更前强制备份 / 启动快照 / 运行时定时快照，含回档往返演练与自动化测试
- [x] 清空演示数据，进入试用（2026-11-13 18:20）
- [x] `.env` 配置监听 IP / 端口（`BIND_HOST` / `BIND_PORT`，`-addr` 优先），启动日志打印局域网地址
- [x] 访问口令保护（Cookie / URL / 请求头三种方式，失败限速），公网暴露前必须开启
- [x] 手机端极简页：手机访问自动进入，只做「拍照 → 识别 → 保存」
- [x] Cloudflare 隧道公网访问（固定 HTTP/2，`scripts/tunnel.sh` 一键启动）
- [x] 一键启动脚本 `scripts/start.sh`：按需编译 → 起服务 → 建隧道 → 打印地址与口令，支持 `--local` / `--stop` / `--restart`
- [x] 分类树 v2：新增「汽车」领域（加油 / 停车费 / 保养 / 维修 / 保险），带版本化迁移与升级前自动备份
- [x] 库存品类修复 v3：显式选品类优先、药品剂型词兜底、编辑可改品类、历史脏数据自动纠正
- [ ] 试用反馈 + 待确认问题 Q2–Q9（见 `docs/README.md`）
- [ ] 增量 3：按反馈调整 + 页面/APK 自动取账单、微信推送提醒

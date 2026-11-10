# ARTEX × Shannon 能力整合方案（白盒 / Web & API 测试 · SSO 登录接管 · InfoSec Portal 工具）

> 目标：改造 ARTEX，把 Shannon 对 **Web 应用 / API** 的测试能力（含**白盒源码分析**）原生重写进 ARTEX；
> 支持需要 **SSO 登录**的内部系统（人工在内置浏览器完成登录后，Agent 继续测试）；
> 并让 ARTEX 能很好地**调用 InfoSec Portal 的工具类能力**（IP 信息、资产信息等）。
>
> ARTEX 保持为**独立项目**；InfoSec Portal 仅作为外部工具被引用，不产生强耦合。

---

## 0. 结论先行（架构决策）

- **做法**：**原生重写**。不引入 Node/Temporal，不把 Shannon 的 TypeScript 代码搬进来；而是把 Shannon 的
  **方法论**（阶段流水线、按漏洞类别的检测/利用策略、白盒"代码路径规则"、证据即报告原则）
  **翻译成 ARTEX 原生的 skill（Markdown 提示词 + 脚本）+ custom tool + 少量 Go 代码**。
- **依据**：两边都是 **LLM 提示词驱动**的 Agent 系统。ARTEX 已具备 skills / custom tools(command·script·http) /
  MCP / 内置 Playwright 浏览器 / 流量录制 / 人在环路审批 等扩展点，Shannon 的能力可以自然落到这些机制上，
  可维护、无额外运行时、与 ARTEX 现有 planner/worker 协同。

### 两边架构对照

| 维度 | Shannon (TS/pnpm/Temporal) | ARTEX (Go/Next.js) |
|---|---|---|
| 编排 | Temporal 流水线：recon → SAST(capella) → per-class vuln → task-formation → per-class exploit → reconciliation → report | 事件驱动 planner + worker，共享 asset 图 / exploration 图，goal 分解 |
| 白盒 | capella SAST（calibrate→threat_model→research→plan→triage→dedupe→confirm→critic→review） | 暂无专门白盒；有 skills 机制可承载 |
| Web/API | 按类别 prompt：injection / auth / authz / ssrf / xss | 已有 `api-recon`、`playwright-cli` skills，流量录制 |
| 登录 | 配置化自动登录（form/SSO，凭据在 config） | 内置 playwright-cli 支持 `state-save/load`、命名会话；有人在环路审批 |
| 扩展 | 代码内置 | `create_skill` / `create_custom_tool` / `create_mcp` 平台工具 |

### Shannon 覆盖面（待移植的漏洞类别 — 证据即报告）
Broken Authentication · Broken Authorization · Injection · XSS · SSRF。
原则：**No exploit, no report** —— 不能用可复现 PoC 证明的发现不进最终报告。

---

## 工作流 A：白盒 + Web/API 测试能力（核心）

把 Shannon 的流水线重写为 ARTEX 的一组 **skills**（供 planner/worker 按需调用），
并新增**源码接入 + 代码分析**这一白盒维度，让有代码的内部系统享受代码分析优势，没代码的退化为黑盒。

### A1. 源码接入层（白盒前置）
- 新增 custom tool / 轻量 Go helper：
  - `source_attach`：登记目标系统的代码仓库（git URL / 本地路径），只读检出到任务工作目录。
  - `code_search` / `code_read`：在已检出代码内做 grep/glob/读取（复用 ARTEX 现有 Bash/文件能力，按作用域限制）。
- 在 task 元数据上增加"是否有源码/源码路径"字段，planner 据此决定走白盒还是黑盒路径。

### A2. 白盒 SAST skill（移植 capella 方法论）
- 新建 skill：`skills/code-sast/`（SKILL.md + references）：
  - **阶段**：校准(calibrate) → 威胁建模(threat_model) → 研究(research) → 计划(plan) →
    初筛(triage) → 去重(dedupe) → 确认(confirm) → 批判(critic) → 复核(review)。
  - 移植 Shannon 的 `shared/_code-path-rules.txt`（FOCUS/AVOID 作用域路由）、
    `sast-enrichment-*` 过程，输出结构化"疑似漏洞点 + 代码位置 + 攻击路径假设"。
- 产出写入 ARTEX 的 **exploration 图 / findings**，作为后续利用阶段的输入（对应 Shannon 的 task-formation）。

### A3. 按漏洞类别的 Web/API 检测 + 利用 skills（移植并本地化）
为每一类新建一个 skill，内含：检测要点、白盒代码特征、运行时验证步骤、利用与 PoC 规范、严重度推理、边界禁止项：
- `skills/web-injection/`（SQLi / 命令注入 / 模板注入 等）
- `skills/web-xss/`
- `skills/web-authz/`（越权 / IDOR）
- `skills/web-auth/`（认证缺陷）
- `skills/web-ssrf/`
- 复用 Shannon：`vuln-*.txt`、`exploit-*.txt`、`task-formation-*.txt`、`_severity-reasoning.txt`、
  `_reporting-standards.txt`、`_exploit-scope.txt`、`_rules-of-engagement.txt`。
- 每个 skill 都遵循 ARTEX 现有 skill 风格（像 `api-recon` 那样带"边界与禁止/完成定义"），
  并接入人在环路审批与流量录制做证据。

### A4. 流水线编排（对齐 ARTEX planner）
- 不引入 Temporal。用 ARTEX 的 **goal 分解** 把一次 Web/API 测试分解为阶段性 goal：
  `侦察 → (有源码?→白盒SAST) → 按类别检测 → 利用验证(PoC) → 汇总去重 → 报告`。
- 在 planner 提示词/promptcatalog 中登记这些 skill 的用途，让 planner 在相应阶段自动选用。
- 报告沿用 ARTEX `report/` + 移植 `report-executive` 结构，坚持"证据即报告"。

### A5. 交付物
新增 skills 目录若干、`source_attach`/`code_*` 工具、planner 阶段引导更新、报告模板增强、以及各 skill 的单元/集成测试与一个样例靶场跑通记录。

### A 实现状态 ✅（原生 skills 重写）
把 Shannon 方法论原生改写为 ARTEX skills(原创措辞,移植其结构与严谨度):
- `web-app-pentest`:总纲——阶段流程(侦察/画像 → 判断白盒/黑盒 → 按类别检测 → 利用验证出 PoC → 报告)、
  证据即报告、代码为基准、只走带内、遵守 ROE 强约束;`references/methodology.md` 收录严重度四问+下限、报告规范、利用边界、白盒要点。
- `code-sast`:白盒源码分析(校准→威胁建模→作用域路由→source→sink 研究→初筛→确认→去重→批判/复核),
  获取源码(git 只读克隆)、产出"可疑点+代码位置+攻击路径假设"交给运行时验证。
- 五类:`web-injection` / `web-xss` / `web-authz` / `web-auth` / `web-ssrf`——各含白盒信号(source/sink)、
  黑盒检测、按 ROE 的利用验证、类别严重度要点、边界。
- 技能可见性(worker/auto/pentest)已注册;内置场景 goal 引用这些 skill。
- **源码接入**:采用 skill 内 `git clone` 只读 + 现有 Bash/grep/read 能力(未新增专用 Go 工具,保持原生 skills 路线)。
- **仍可增强(后续)**:报告模板按类别细化、专用 `source_attach` 工具/UI、样例靶场回归记录。

---

## 工作流 B：SSO 登录接管（人工登录 → Agent 续测）

很多内部系统需要 SSO（可能含 MFA/推送），无法用静态凭据自动登录。做成**人在环路的登录接管**：

### B1. 机制
- 复用 `playwright-cli` 的**命名会话**与 `state-save` / `state-load`：
  1. Agent 需要已登录会话时，发起一个 **"等待人工登录"审批/暂停**（复用 ARTEX intercept/审批与任务暂停机制）。
  2. 人工在**内置浏览器会话**里完成 SSO 登录（可处理账号选择、MFA、consent）。
  3. 人工点击"登录完成/继续"→ Agent `state-save` 保存 storage-state，校验登录成功条件。
  4. 后续所有 worker 以该命名会话 / `state-load` 复用认证态继续测试；会话过期可再次触发接管。
- 新建 skill：`skills/authenticated-session/`，移植 Shannon `login-instructions.txt` 的**成功校验**与
  `validate-authentication` 逻辑，但把"自动填凭据"替换为"**人工接管 + 成功校验 + 保存会话**"。

### B2. 需要的改动（Go 侧）
- 任务控制增加一个"**等待人工登录**"的暂停/恢复状态（复用 `task_control.go` / intercept 审批 UI）。
- 前端在该状态下给出"打开浏览器登录 / 我已登录完成"操作入口（复用现有内置浏览器与审批卡片组件）。
- 会话态文件按任务隔离存储，供 worker 复用。

### B3. 交付物
`authenticated-session` skill、任务暂停/恢复的登录接管状态与 API、前端接管入口、会话复用与过期重登流程、测试。

### B 实现状态 ✅（基础设施需真机验证）
采用 **noVNC + 有头 Chromium**。详见 `design/sso-login.md`。
- 后端：`server/browser_login.go`（阻塞式 `browser_login` 工具 + 待处理接管管理 + API），
  config `browser_login`（含环境变量覆盖），单元测试通过。
- 基础设施：`scripts/browser-session.sh`（Xvfb+x11vnc+noVNC+有头 Chromium@CDP）、`start.sh` 钩子、
  `Dockerfile`（装 VNC 栈 + chromium、暴露 6080）、CDP 助手 `cdp-helper.js`。
- 技能：`skills/authenticated-session/`。前端：全局登录接管面板(noVNC iframe + “我已登录完成”)。
- **可选**：`enabled=false`（默认）零影响。**⚠️ VNC/浏览器运行时需在真实部署验证**（沙箱无显示设备）。

---

## 工作流 C：InfoSec Portal 工具整合（弱耦合）

让 ARTEX 在测试中能调用 Portal 的工具类能力（如：查 IP 归属/情报、查资产信息等），但 ARTEX 仍独立。

### C1. 两种接入方式（二选一或并存）
- **首选：MCP** —— 若 Portal 能提供/包一层 MCP 端点，用 ARTEX 的 `create_mcp`（http/sse/stdio）接入，
  Portal 的每个工具自动变成 Agent 可调用的 tool。统一、解耦、最易维护。
- **备选：custom http tool** —— 对 Portal 每个 REST 接口用 `create_custom_tool`(kind=http) 登记，
  配置 URL/headers/参数 schema（鉴权走 header/token，注意机密管理）。
- 把这些工具绑定到合适的 agent，并在 skill/prompt 中说明何时调用（例如发现一个 IP/域名/资产时自动 enrich）。

### C2. 需要你确认的接入信息（实现 C 时提供）
- Portal 的调用方式（是否有 MCP？否则 REST base URL + 各接口）、鉴权方式（token/SSO/mTLS）、
  可用工具清单（IP 信息、资产信息、其他）、速率/配额限制。

### C3. 交付物
Portal 工具的 MCP 或 custom-http 登记、与 ARTEX asset/enrich 流程的挂接（发现资产时自动富化）、
机密配置方式文档、调用示例与测试。

### C（第一阶段）实现状态 ✅
已完成并可用（按"最高执行效率"采用原生 HTTP 工具，复用 `runHTTPTool`）：
- `config.InfoSecPortal`：可选配置段 + 环境变量覆盖（`ARTEX_INFOSEC_PORTAL_BASE_URL/TOKEN`）。
- `server/infosec_portal.go`：按配置构建可调用工具 + seed 工具表行（可在界面按 agent 调整绑定）。
- httpExec 增加 `insecure`（内网自签证书）。
- `config.example.json` 示例、`design/infosec-portal.md` 文档、单元测试。
- **未配置 = 零影响**：不注册、不 seed，ARTEX 行为与未实现该功能完全一致。

---

## 工作流 C（第二阶段）：域名清单同步 + 场景化发起测试

把 Portal 的 **domain inventory** 同步进 ARTEX，并让用户"点域名 + 选场景"即可发起测试，
免去每次手写任务描述。（复用 ARTEX 已有的 **ScopeSentry 资产同步** 与 **任务模板** 两套机制。）

### C-II.1 域名清单同步（inventory sync）
- 参照 `server/sync_scopesentry.go` 的模式，从 InfoSec Portal 拉取 domain inventory →
  映射为 ARTEX 公司 + 资产（域名/子域名）。数据源沿用 `infosec_portal` 配置（REST）或 MCP。
- 支持增量同步、去重、入库上限保护；在资产库/清单页展示。
- **需要你提供**：Portal 列 domain inventory 的接口（REST path + 返回结构，或 MCP 工具名）。

### C-II.2 测试场景（scenarios，预制 + 多选）
- 场景 = 预制的测试意图，映射到 ARTEX **任务模板**（已有：name/description/goal/category/intercept_rules）。
- 预制若干场景（例：Web 常规漏扫、API 侦察、认证/越权专项、SSRF 专项、白盒源码审计…），
  每个场景携带现成的 goal/描述模板。
- 发起时：**可多选场景** → 合成任务 goal/描述；用户还可**叠加额外补充说明**。
  - 实现：新增一个"按场景+目标合成任务"的后端端点（把所选模板的 goal 合并 + 追加用户补充 + 绑定目标资产），
    前端出一个"选择场景（多选）+ 补充说明"的发起对话框。

### C-II.3 从清单一键发起
- 在 inventory/资产页，点某 domain → 弹出 C-II.2 的发起对话框（预填该 domain 为目标）→ 建任务。

### C（第二阶段）交付物
Portal inventory 同步器 + 页面、场景（任务模板）预制与"多选场景+补充说明"合成发起端点、
清单"点域名发起测试"前端流、测试。

### C（第二阶段）实现状态 ✅
- **后端**：`server/portal_inventory.go`（provider 抽象 + **mock** + 真实 Portal HTTP provider + 宽松解析 + 同步到资产库）、
  `server/scenarios.go`（内置场景幂等 seed + 列表 + "多选场景+补充说明"合成发起）；
  config 增加 `infosec_portal.inventory`；路由与启动 seed 已接；单元测试(解析/mock/合成)通过。
- **前端**：新页面 `web/.../function/inventory`（域名清单 + 同步 + "发起测试"对话框：多选场景 + 补充说明 + 任务名），
  api 方法与类型、侧边栏入口已加；`tsc` 与 biome 通过。
- **inventory 接口未定义**：默认用内置 mock，配置 `inventory.path` 即切换真实接口（provider 可替换）。
- **仍需你提供**：真实 Portal inventory 接口（path + 返回结构），接上后 mock 自动让位。

---

## 落地顺序（建议）

1. **C（Portal 工具）** —— 最小改动、纯扩展、能立刻提升现有测试的信息富化能力（依赖你提供接入信息）。
2. **B（SSO 接管）** —— 解锁"内部系统能真正被测"的前提，改动集中在浏览器会话 + 任务暂停/恢复。
3. **A（白盒 + Web/API）** —— 最大工作量，分批移植：先 `api-recon` 已有 → 按类别 skills → 白盒 SAST → 流水线编排 → 报告。

> A 可与 B/C 并行推进（skills 与工具相互独立）。每个工作流独立成 PR，便于你分别 review。

---

## 增强项(对标 Shannon 补齐)✅

在 C/B/A 之外,又补齐了几项 Shannon 开源版有、ARTEX 原先没有的能力:

- **自动化凭据登录**(`auto_login`):与 B 人工接管互补,配置 `auth_profiles`(表单步骤 + `$username/$password/$totp`,凭据 `${ENV}` 注入),RFC 6238 TOTP,无头 playwright 执行并保存会话态。未配置零影响。
- **持久化 SAST KB + 结构化 task-formation + reconciliation**:`code-sast` 产出可累积知识库(`sast-kb/`)与结构化利用任务清单;`web-app-pentest` 方法论加入"观察→任务→验证回填→归并去重→一洞一发现"。
- **PDF 报告 + LLM 执行摘要**:`/api/report/executive`(LLM 合成执行摘要)、`/api/report/html`(自包含可打印 HTML)、`/api/report/pdf`(无头 Chromium 打印);报告页加 HTML/PDF 导出按钮。

> 暂未做(可后续):SARIF 2.1.0 输出/摄入、CI/CD 一次性扫描模式(GitHub Action/GitLab)。

## 风险与注意

- **授权边界**：全部能力仅用于**已授权**的内部系统测试；保留 ARTEX/Shannon 的"边界与禁止项"，默认开启人在环路审批，破坏性/越权操作需审批。
- **机密管理**：Portal token、登录会话态按任务隔离、不写入代码/日志/报告。
- **方法论移植 ≠ 逐行翻译**：以行为对齐 + 靶场回归验证为准，而非照搬 TS 结构。
- **ARTEX 独立性**：Portal 通过 MCP/http 弱耦合接入，不在 ARTEX 里硬编码 Portal 业务逻辑。

---

## 需要你拍板/提供的

1. 工作流 **C** 的 Portal 接入信息（见 C2）。
2. **A** 的漏洞类别优先级（是否就按 Shannon 五类：injection/xss/authz/auth/ssrf，还是先做其中几类）。
3. 是否有一个**可用的授权靶场/内部测试目标**用于回归验证。
4. 落地顺序是否采用上面建议（C → B → A，A 可并行）。

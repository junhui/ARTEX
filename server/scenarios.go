package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// 测试场景(scenarios)(方案 工作流 C 第二阶段)。
//
// 场景 = 预制的测试意图,复用 ARTEX 的「任务模板」承载(name/description/goal/拦截规则)。
// 目标:免去每次手写任务描述——用户在清单里点一个域名,勾选一个或多个场景(可多选),
// 可再叠加额外补充说明,即可一键建任务。场景也可由用户在「任务模板」里自行增删。

// ---------- 约束等级(ROE 强约束) ----------

// scenarioConstraint is a selectable rules-of-engagement intensity. Its ROE text
// is injected prominently into the task goal as hard, non-negotiable rules, so the
// same scenarios can be run safely (read-only proof) or more intrusively (with
// approval). This is the "强约束选项" — e.g. 严格不改数据 → safe_validate.
type scenarioConstraint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	ROE  string `json:"roe"` // injected into the goal as hard rules
}

func scenarioConstraints() []scenarioConstraint {
	return []scenarioConstraint{
		{
			ID:   "recon_only",
			Name: "只读侦察",
			Desc: "只做信息采集与枚举，不触发任何漏洞利用。",
			ROE: "本次为【只读侦察】：只做信息采集、指纹、枚举与读取公开响应。" +
				"严禁发送任何利用性 payload、严禁任何改动状态的操作(增/改/删/建账号/上传/批量)、严禁 DoS。" +
				"只记录观察到的事实与可疑点，不做利用验证。",
		},
		{
			ID:   "safe_validate",
			Name: "安全验证 · 严格不改数据(默认)",
			Desc: "用非破坏、只读方式证明漏洞；严格不改动任何数据。",
			ROE: "本次为【安全验证 · 严格不改数据】：可做漏洞利用性验证，但【只允许只读、非破坏】的 PoC。" +
				"允许：越权/未授权【读取】证明、反射型 XSS 的无害弹窗(alert(document.domain))、" +
				"布尔/只读型注入的存在性证明、读取不该暴露的数据作为证据、截图取证。" +
				"严禁：任何【改动数据】的操作——增/改/删记录、建/改/删账号或权限、提交表单写入、" +
				"上传、持久化(存储型)落库、支付/下单、发送通知/邮件、批量操作、以及任何不可逆或影响可用性的动作。" +
				"若某漏洞只能通过改动数据才能证明，则【不要执行】，改为依据代码/响应/错误信息进行推断并清楚标注“未实际改动、属推断”。",
		},
		{
			ID:   "standard",
			Name: "常规渗透 · 风险操作需审批",
			Desc: "允许利用；改动型/高风险操作走人在环路审批。",
			ROE: "本次为【常规渗透】：允许漏洞利用。只读验证可直接做；" +
				"任何【改动数据】或高风险操作必须先经人在环路审批后再执行，并优先选择影响最小的方式(如建少量可清理的良性测试数据)。" +
				"严禁：破坏性/不可逆/批量删改、影响生产可用性(DoS)、泄露或外传敏感数据。",
		},
		{
			ID:   "intrusive",
			Name: "深度 · 含改动(每步显式审批)",
			Desc: "允许改动型利用，但每步需显式审批。",
			ROE: "本次为【深度测试】：允许改动型利用(如越权写、存储型 XSS 落库验证)，但【每一个改动型步骤都必须先获得显式审批】，" +
				"并记录改动内容以便回滚。严禁：破坏性批量删改、删库、勒索式操作、影响生产可用性、外传敏感数据。",
		},
	}
}

const defaultConstraintID = "safe_validate"

func constraintByID(id string) *scenarioConstraint {
	id = strings.TrimSpace(id)
	if id == "" {
		id = defaultConstraintID
	}
	for i := range scenarioConstraints() {
		if scenarioConstraints()[i].ID == id {
			c := scenarioConstraints()[i]
			return &c
		}
	}
	return nil
}

// domainProfileDirective tells the agent to profile the target while testing and
// record it as reviewable evidence. Included by default (profile=true) because many
// targets are freshly-discovered domains whose purpose is unknown.
const domainProfileDirective = "【域名画像采集 · 测试时顺带完成】对目标域名做一次画像并【记录为可复盘的信息】" +
	"(用 report_finding 记为一条 info/recon 级发现，关键页面用 playwright-cli 截图存到工作目录)。至少覆盖：\n" +
	"1) 这个站点是做什么的、面向谁(内部员工/合作伙伴/公众)；\n" +
	"2) 是否需要登录、认证方式(SSO/OAuth/表单/Basic)、SSO 提供方、是否 MFA；\n" +
	"3) 登录前后能看到什么数据、是否含敏感数据(PII/财务/凭据等)及数据分级线索；\n" +
	"4) 技术栈/指纹(服务器/框架/前端/CDN/WAF)、主要功能与 API 面、上传/搜索/后台迹象；\n" +
	"5) 暴露面与归属(可调用 portal_ip_lookup / portal_asset_lookup 富化 IP/资产信息；结合清单里的 owner/业务线)；\n" +
	"6) 风险初判(显眼的配置问题、版本、调试信息、目录列举、默认页等)。\n" +
	"鼓励补充其它有价值的信息。关键页面(首页/登录页/登录后主页/敏感功能页)请截图取证。"

// builtinScenarios are seeded as task templates on startup (idempotent). They are
// ordinary templates afterwards, so users can edit/extend them in the UI.
func builtinScenarios() []db.TaskTemplateInput {
	return []db.TaskTemplateInput{
		{
			Name:        "Web 应用常规测试",
			Description: "对目标 Web 应用做常规安全测试",
			Goal: "对目标 Web 应用进行常规安全测试,覆盖注入、XSS、认证缺陷、越权/IDOR、SSRF、" +
				"敏感信息泄露、配置不当等常见风险。按 web-app-pentest 总纲的流程推进,按类别用 " +
				"web-injection / web-xss / web-authz / web-auth / web-ssrf;有源码时先用 code-sast 白盒分析。" +
				"遵循人在环路审批,高风险/破坏性操作先审批;仅针对已授权目标;以可复现的 PoC 作为漏洞证据(证据即报告)。",
		},
		{
			Name:        "API 接口侦察与测试",
			Description: "侦察并测试目标 API 面",
			Goal: "尽可能完整地发现目标的 API 接口(路径、方法、参数、响应),梳理鉴权与权限模型," +
				"在已授权前提下测试注入、越权、认证绕过、批量数据访问等 API 常见问题。只针对授权目标," +
				"避免不可逆的破坏性操作。",
		},
		{
			Name:        "认证与会话安全专项",
			Description: "认证、会话、口令策略专项",
			Goal: "针对目标的认证与会话管理做专项测试:登录/登出、会话固定与失效、口令策略、" +
				"多因素、找回密码、记住我、JWT/Cookie 安全属性、暴力破解防护等。只验证,不做大规模爆破。",
		},
		{
			Name:        "越权 / IDOR 专项",
			Description: "水平/垂直越权专项",
			Goal: "针对目标的访问控制做越权专项:水平越权(访问他人数据)、垂直越权(低权限触达高权限功能)、" +
				"IDOR(可预测对象引用)。用两个不同权限账号对比验证,以可复现证据说明影响。",
		},
		{
			Name:        "SSRF 专项",
			Description: "服务端请求伪造专项",
			Goal: "针对目标中会发起服务端请求的功能(URL 抓取、Webhook、导入、预览、文件/图片拉取等)做 SSRF 专项," +
				"验证能否访问内网/元数据服务。仅做可控验证,不触碰敏感内网资产。",
		},
		{
			Name:        "域名画像 / 信息采集",
			Description: "只做信息采集：这个域名是做什么的、是否 SSO、能看到什么数据",
			Goal: "对目标域名做一次信息采集画像(不做漏洞利用):梳理它的用途与受众、是否需要登录及认证方式(是否 SSO/MFA)、" +
				"登录前后能看到哪些数据及是否敏感、技术栈与主要功能/接口面、暴露面与归属,并对关键页面截图取证," +
				"把结果记录为可复盘的信息。鼓励补充其它有价值的发现。",
		},
		{
			Name:        "白盒源码审计",
			Description: "结合源码的白盒审计(需提供代码)",
			Goal: "在提供源码的前提下进行白盒审计:用 code-sast 做威胁建模与 source→sink 分析," +
				"按漏洞类别(注入、越权、认证、SSRF、XSS 等)定位可疑代码点与攻击路径," +
				"再结合 web-app-pentest 在运行环境中以 PoC 验证。无源码时退化为黑盒。",
		},
	}
}

// seedBuiltinScenarios inserts the built-in scenario templates if absent. Idempotent
// via the templates table's ON CONFLICT(nkey) DO NOTHING (returns a name-conflict
// error we treat as "already present").
func (s *Server) seedBuiltinScenarios() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	for _, sc := range builtinScenarios() {
		if _, err := s.m.pg.CreateTaskTemplate(sc); err != nil {
			if errors.Is(err, db.ErrTaskTemplateNameConflict) {
				continue // already seeded / user already has one with this name
			}
			log.Printf("[scenarios] seed %q 失败: %v", sc.Name, err)
		}
	}
}

// ---------- GET /api/scenarios ----------
// Scenarios are task templates; this is a named view for the launch UI. It also
// returns the selectable constraint levels and the default constraint.

func (s *Server) listScenarios(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	rows, err := s.m.pg.ListTaskTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"scenarios":          rows,
		"constraints":        scenarioConstraints(),
		"default_constraint": defaultConstraintID,
	})
}

// ---------- POST /api/scenarios/launch ----------
// Compose a task from a target + selected scenarios (+ optional extra text) and
// launch it, so users don't hand-write task descriptions each time.

type scenarioLaunchReq struct {
	Target        string  `json:"target"`          // domain/host/url under test
	ScenarioIDs   []int64 `json:"scenario_ids"`    // selected scenario (template) ids; multi-select
	Constraint    string  `json:"constraint"`      // ROE level id (default safe_validate)
	Profile       *bool   `json:"profile"`         // also profile the domain while testing (default true)
	Extra         string  `json:"extra"`           // optional additional instructions
	Name          string  `json:"name"`            // optional task name
	CategoryID    *int64  `json:"category_id"`     // optional task category
	CompanyIDs    []int64 `json:"company_ids"`     // optional company association
	LLMProfileIDs []int64 `json:"llm_profile_ids"` // optional LLM profile(s)
}

func (s *Server) launchScenario(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	var req scenarioLaunchReq
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	req.Extra = strings.TrimSpace(req.Extra)
	if req.Target == "" {
		writeErr(w, 400, "target 不能为空")
		return
	}
	if len(req.ScenarioIDs) == 0 && req.Extra == "" {
		writeErr(w, 400, "至少选择一个场景,或填写补充说明")
		return
	}

	// Collect selected scenario templates (dedup, preserve order).
	seen := map[int64]bool{}
	var tmpls []*db.TaskTemplate
	for _, id := range req.ScenarioIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		t, err := s.m.pg.GetTaskTemplate(id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if t == nil {
			writeErr(w, 400, fmt.Sprintf("场景 #%d 不存在", id))
			return
		}
		tmpls = append(tmpls, t)
	}

	constraint := constraintByID(req.Constraint)
	if constraint == nil {
		writeErr(w, 400, "无效的约束等级(constraint)")
		return
	}
	profile := req.Profile == nil || *req.Profile // default true
	goal, interceptRules := composeScenarioGoal(req.Target, req.Extra, tmpls, constraint, profile)
	// 名称与描述共用:用户填了就用,否则回落「场景测试：<目标>」。此前只给 description
	// 兜底、Name 直接用空 req.Name,导致从清单发起的任务显示为「未命名」。
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "场景测试：" + req.Target
	}
	description := name

	if len(req.LLMProfileIDs) > 0 {
		if err := s.validateTaskProfileIDs(req.LLMProfileIDs); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	companyIDs, err := db.NormalizeTaskCompanyIDs(req.CompanyIDs)
	if err != nil {
		writeErr(w, 400, fmt.Sprintf("关联企业无效：最多选择 %d 个有效企业", db.MaxTaskCompanyCount))
		return
	}

	t, err := s.m.CreateTaskWithOptions(description, goal, db.TaskCreateOptions{
		Name:           name,
		CategoryID:     req.CategoryID,
		CompanyIDs:     companyIDs,
		LLMProfileIDs:  req.LLMProfileIDs,
		InterceptRules: interceptRules,
	})
	if err != nil {
		if errors.Is(err, db.ErrTaskCategoryInvalid) || errors.Is(err, db.ErrTaskCategoryNotFound) {
			writeErr(w, 400, "任务分类不存在或无效")
			return
		}
		if errors.Is(err, db.ErrTaskCompanyIDsInvalid) || errors.Is(err, db.ErrTaskCompanyNotFound) {
			writeErr(w, 400, "关联企业不存在或无效")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("[scenarios] 场景发起任务 #%s 目标: %s (%d 场景)", t.ID, req.Target, len(tmpls))
	s.launchTask(t, description+" "+goal, false)
	writeJSON(w, 201, taskDTO(t, s.resolvedTaskStatus(t)))
}

// composeScenarioGoal merges the ROE constraint, target, optional domain-profiling
// directive, the selected scenarios' goals, and the user's extra notes into one task
// goal, and unions the scenarios' intercept rules.
func composeScenarioGoal(target, extra string, tmpls []*db.TaskTemplate, c *scenarioConstraint, profile bool) (string, []db.TaskInterceptRuleInput) {
	var b strings.Builder
	if c != nil {
		fmt.Fprintf(&b, "【强约束 · 不可违反】%s\n\n", c.ROE)
	}
	fmt.Fprintf(&b, "测试目标：%s\n", target)
	if profile {
		fmt.Fprintf(&b, "\n%s\n", domainProfileDirective)
	}
	for _, t := range tmpls {
		fmt.Fprintf(&b, "\n## 场景：%s\n%s\n", t.Name, strings.TrimSpace(t.Goal))
	}
	if extra != "" {
		fmt.Fprintf(&b, "\n## 额外说明\n%s\n", extra)
	}

	var rules []db.TaskInterceptRuleInput
	ruleSeen := map[string]bool{}
	for _, t := range tmpls {
		for _, rr := range t.InterceptRules {
			k := rr.Action + "|" + rr.Kind + "|" + rr.Pattern
			if ruleSeen[k] {
				continue
			}
			ruleSeen[k] = true
			rules = append(rules, rr)
		}
	}
	return b.String(), rules
}

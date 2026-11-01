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

// builtinScenarios are seeded as task templates on startup (idempotent). They are
// ordinary templates afterwards, so users can edit/extend them in the UI.
func builtinScenarios() []db.TaskTemplateInput {
	return []db.TaskTemplateInput{
		{
			Name:        "Web 应用常规测试",
			Description: "对目标 Web 应用做常规安全测试",
			Goal: "对目标 Web 应用进行常规安全测试,覆盖注入、XSS、认证缺陷、越权/IDOR、SSRF、" +
				"敏感信息泄露、配置不当等常见风险。遵循人在环路审批,高风险/破坏性操作先审批;" +
				"仅针对已授权目标;以可复现的 PoC 作为漏洞证据(证据即报告)。",
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
			Name:        "白盒源码审计",
			Description: "结合源码的白盒审计(需提供代码)",
			Goal: "在提供源码的前提下进行白盒审计:威胁建模、按漏洞类别审查代码路径(注入、越权、认证、SSRF、XSS 等)," +
				"定位可疑代码点与攻击路径,并在运行环境中以 PoC 验证。无源码时退化为黑盒。",
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
// Scenarios are task templates; this is a named view for the launch UI.

func (s *Server) listScenarios(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	rows, err := s.m.pg.ListTaskTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"scenarios": rows})
}

// ---------- POST /api/scenarios/launch ----------
// Compose a task from a target + selected scenarios (+ optional extra text) and
// launch it, so users don't hand-write task descriptions each time.

type scenarioLaunchReq struct {
	Target        string  `json:"target"`         // domain/host/url under test
	ScenarioIDs   []int64 `json:"scenario_ids"`   // selected scenario (template) ids; multi-select
	Extra         string  `json:"extra"`          // optional additional instructions
	Name          string  `json:"name"`           // optional task name
	CategoryID    *int64  `json:"category_id"`    // optional task category
	CompanyIDs    []int64 `json:"company_ids"`    // optional company association
	LLMProfileIDs []int64 `json:"llm_profile_ids"`// optional LLM profile(s)
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

	goal, interceptRules := composeScenarioGoal(req.Target, req.Extra, tmpls)
	description := strings.TrimSpace(req.Name)
	if description == "" {
		description = "场景测试：" + req.Target
	}

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
		Name:           strings.TrimSpace(req.Name),
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

// composeScenarioGoal merges the target, the selected scenarios' goals, and the
// user's extra notes into one task goal, and unions the scenarios' intercept rules.
func composeScenarioGoal(target, extra string, tmpls []*db.TaskTemplate) (string, []db.TaskInterceptRuleInput) {
	var b strings.Builder
	fmt.Fprintf(&b, "测试目标：%s\n", target)
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

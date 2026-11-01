package server

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"sort"
	"strings"

	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// InfoSec Portal 集成(方案 工作流 C):把内部 InfoSec Portal 的工具类能力(查 IP 信息、
// 查资产信息等)作为 HTTP 工具暴露给 Agent。完全由配置驱动且可选——config.json 不配置
// infosec_portal.base_url 时,本模块不注册任何工具、不 seed 任何行,ARTEX 行为与未实现
// 该功能完全一致(顶多这些查询工具不可用)。执行层复用自定义工具的 runHTTPTool(原生
// HTTP、无额外进程/MCP 往返),是效率最优的接入方式。ARTEX 仍为独立项目,Portal 仅经
// 普通 HTTP 被引用,不在代码里硬编码 Portal 业务逻辑。

// defaultPortalAgents is the fallback agent binding when neither the tool nor the
// portal section names agents. The lookup tools are useful during planning,
// per-worker exploitation, and the Auto agent, so bind to those by default.
func defaultPortalAgents() []string { return []string{"planner", "worker", "auto"} }

// infosecPortalTools builds in-memory CoreTools for every configured portal tool.
// Returns nil when the portal is not configured — zero impact on the agent runtime.
func (s *Server) infosecPortalTools() []actool.CoreTool {
	return s.infosecPortalToolsFrom(config.InfoSecPortalConfig())
}

// infosecPortalToolsFrom is the testable core: it builds tools from an explicit
// config instead of reading disk.
func (s *Server) infosecPortalToolsFrom(cfg config.InfoSecPortal) []actool.CoreTool {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" || len(cfg.Tools) == 0 {
		return nil
	}
	var out []actool.CoreTool
	for _, pt := range cfg.Tools {
		if pt.Enabled != nil && !*pt.Enabled {
			continue
		}
		if strings.TrimSpace(pt.Key) == "" {
			continue
		}
		execRaw, err := json.Marshal(buildPortalHTTPExec(base, cfg, pt))
		if err != nil {
			log.Printf("[infosec-portal] 构建工具 %s 失败: %v", pt.Key, err)
			continue
		}
		key, desc := pt.Key, pt.Description
		schema := ensureSchema(pt.Schema)
		run := func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			var params map[string]any
			if len(in) > 0 {
				_ = json.Unmarshal(in, &params)
			}
			if params == nil {
				params = map[string]any{}
			}
			return s.runHTTPTool(ctx, execRaw, params, tc)
		}
		out = append(out, actool.Build(actool.Spec{
			Name: key, Description: desc, Schema: schema,
			Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				return permission.Allowed()
			},
			Run: run,
		}))
	}
	return out
}

// buildPortalHTTPExec folds the portal base URL + per-tool path/query/headers/body
// into the httpExec shape runHTTPTool understands. {param} placeholders are left
// intact here and rendered from the tool call's arguments at execution time.
func buildPortalHTTPExec(base string, cfg config.InfoSecPortal, pt config.InfoSecPortalTool) httpExec {
	method := strings.ToUpper(strings.TrimSpace(pt.Method))
	if method == "" {
		method = "GET"
	}
	u := base + "/" + strings.TrimLeft(strings.TrimSpace(pt.Path), "/")
	if len(pt.Query) > 0 {
		keys := make([]string, 0, len(pt.Query))
		for k := range pt.Query {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic URL
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			// Key is escaped; the value may carry a {param} placeholder, so it is
			// left raw to be rendered later (same contract as custom http tools).
			parts = append(parts, url.QueryEscape(k)+"="+pt.Query[k])
		}
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + strings.Join(parts, "&")
	}
	headers := map[string]string{}
	for k, v := range cfg.Headers { // portal-wide (e.g. auth)
		headers[k] = v
	}
	for k, v := range pt.Headers { // per-tool overrides/additions
		headers[k] = v
	}
	return httpExec{
		Method:            method,
		URL:               u,
		Headers:           headers,
		Body:              pt.Body,
		TimeoutMs:         cfg.TimeoutMs,
		Proxy:             cfg.Proxy,
		UseRecordingProxy: cfg.UseRecordingProxy,
		Insecure:          cfg.Insecure,
	}
}

// portalGET issues a GET against the portal (base_url + path) honoring the portal's
// static headers, proxy, and TLS settings, reusing the http-tool executor. It
// returns the response body bytes and HTTP status. Used by the inventory provider.
func (s *Server) portalGET(ctx context.Context, cfg config.InfoSecPortal, method, path string) ([]byte, int, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, 0, errPortalNotConfigured
	}
	if strings.TrimSpace(method) == "" {
		method = "GET"
	}
	exec := httpExec{
		Method:            strings.ToUpper(method),
		URL:               base + "/" + strings.TrimLeft(strings.TrimSpace(path), "/"),
		Headers:           cfg.Headers,
		TimeoutMs:         cfg.TimeoutMs,
		Proxy:             cfg.Proxy,
		UseRecordingProxy: cfg.UseRecordingProxy,
		Insecure:          cfg.Insecure,
	}
	execRaw, _ := json.Marshal(exec)
	res, err := s.runHTTPTool(ctx, execRaw, map[string]any{}, nil)
	if err != nil {
		return nil, 0, err
	}
	// runHTTPTool returns {"status":int,"body":string}; unwrap it.
	var wrap struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if jerr := json.Unmarshal([]byte(res.Flatten()), &wrap); jerr != nil {
		// Not the expected envelope (e.g. transport error text) → surface raw.
		return []byte(res.Flatten()), 0, nil
	}
	return []byte(wrap.Body), wrap.Status, nil
}

var errPortalNotConfigured = errorString("InfoSec Portal 未配置 base_url")

type errorString string

func (e errorString) Error() string { return string(e) }

// seedInfoSecPortalTools seeds a tools-table row per configured portal tool so each
// is bindable per-agent and manageable from the UI (like the built-in/traffic host
// tools). Idempotent (SeedTool DO-NOTHING on conflict) so later UI binding edits
// survive restarts. No-op when the portal is not configured; stale rows left after
// a tool is removed from config are inert (no in-memory tool backs them).
func (s *Server) seedInfoSecPortalTools() {
	cfg := config.InfoSecPortalConfig()
	if strings.TrimSpace(cfg.BaseURL) == "" || len(cfg.Tools) == 0 {
		return
	}
	for _, pt := range cfg.Tools {
		if pt.Enabled != nil && !*pt.Enabled {
			continue
		}
		if strings.TrimSpace(pt.Key) == "" {
			continue
		}
		agents := pt.Agents
		if len(agents) == 0 {
			agents = cfg.Agents
		}
		if len(agents) == 0 {
			agents = defaultPortalAgents()
		}
		schema, _ := json.Marshal(ensureSchema(pt.Schema))
		ab, _ := json.Marshal(agents)
		if err := s.m.pg.SeedTool(pt.Key, pt.Description, schema, ab); err != nil {
			log.Printf("[infosec-portal] seed %s 失败: %v", pt.Key, err)
		}
	}
}

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

// reReqLine parses one line of `playwright-cli --raw requests`, e.g.
//   12. [GET] http://host/rest/x => [200] OK
var reReqLine = regexp.MustCompile(`\[([A-Z]+)\]\s+(\S+)\s+=>\s+\[(\d+)\]`)

// browserReconTimeout bounds the whole deterministic recon pass.
const browserReconTimeout = 100 * time.Second

// isStaticReconPath reports whether a URL path is a static asset we don't want to
// register as an API endpoint (the renderer already hides most static requests, but
// a few — i18n json, source maps — slip through).
func isStaticReconPath(p string) bool {
	p = strings.ToLower(p)
	for _, ext := range []string{".js", ".css", ".map", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf", ".webp", ".mp4", ".json"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return strings.HasPrefix(p, "/socket.io")
}

// browserRecon runs a DETERMINISTIC, model-independent reconnaissance pass: it renders
// the seed target in a real headless browser (playwright-cli + chromium) and harvests
// (a) the backend API endpoints the SPA actually calls at runtime and (b) the frontend
// routes — writing them into the asset graph as endpoints + one summary fact. This way
// even a weak LLM starts the exploration with a real attack surface already mapped,
// instead of only the unrendered HTML shell a single curl would return.
//
// Best-effort and fully bounded: any failure just logs and returns; it never blocks or
// fails task creation. Only runs for http/https targets. Uses a per-task playwright
// session so concurrent tasks don't collide. The target is the task's own authorized
// seed host, so this does not cross the scope boundary.
func (s *Server) browserRecon(t *Task, scheme, host string, port int) {
	if scheme != "http" && scheme != "https" {
		return
	}
	as := s.m.Assets()
	if as == nil {
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)

	base := scheme + "://" + host
	if !(scheme == "https" && port == 443) && !(scheme == "http" && port == 80) {
		base += ":" + strconv.Itoa(port)
	}

	// The task's 会话 (sessions) view is built from intent nodes (one card per intent,
	// plus plan/main). To make the recon visible there as its own session card, anchor
	// it to a dedicated intent node — created state='done' so it never enters the
	// frontier / gets claimed by a worker. All recon activity + the resulting fact hang
	// off this node, so the UI renders the full browser execution process as a session.
	reconIntentID, rerr := t.Store.AddNode(db.KindIntent,
		map[string]any{"summary": "浏览器确定性侦察（引擎自动执行，非 LLM）：渲染目标 → 抓运行时接口/前端路由 → 写入资产图"},
		0, "done", "recon", nil)
	if rerr != nil {
		log.Printf("[recon] task %s 创建侦察会话节点失败（会话卡片可能不显示，不影响侦察）：%v", t.ID, rerr)
	}
	emit := func(kind, tool, summary, detail string, isErr bool) {
		a := db.Activity{Worker: "browser-recon", Kind: kind, Tool: tool, Summary: summary, Detail: detail, IsError: isErr}
		if reconIntentID > 0 {
			a.NodeID = &reconIntentID
		}
		s.engine.emitActivity(t, a)
	}
	emit("round", "", "浏览器确定性侦察：真浏览器渲染目标 → 抓运行时接口 + 前端路由 → 写入资产图", "", false)

	ctx, cancel := context.WithTimeout(context.Background(), browserReconTimeout)
	defer cancel()

	// Route the recon browser through the recording proxy (when capture is on) so its
	// requests are MITM-captured and show up in the task's 流量 tab — same as worker
	// bash/curl traffic. playwright-cli reads these from the environment; the browser
	// daemon (spawned by `open`) inherits them. NODE_EXTRA_CA_CERTS trusts the MITM CA;
	// IGNORE_HTTPS_ERRORS covers self-signed interception on https targets.
	reconEnv := os.Environ()
	if proxy := s.m.ProxyAddr(); proxy != "" {
		if !strings.Contains(proxy, "://") {
			proxy = "http://" + proxy
		}
		reconEnv = append(reconEnv,
			"PLAYWRIGHT_MCP_PROXY_SERVER="+proxy,
			"PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS=true")
		if cert := s.m.ProxyCACert(); cert != "" {
			reconEnv = append(reconEnv, "NODE_EXTRA_CA_CERTS="+cert)
		}
	}

	sid := "recon-" + t.ID
	run := func(args ...string) (string, error) {
		full := append([]string{"-s=" + sid}, args...)
		c := exec.CommandContext(ctx, "playwright-cli", full...)
		c.Env = reconEnv
		out, err := c.CombinedOutput()
		return string(out), err
	}

	emit("tool_use", "playwright-cli", "open --browser=chromium "+base, "", false)
	if out, err := run("open", "--browser=chromium", base); err != nil {
		log.Printf("[recon] task %s 浏览器渲染 %s 失败，跳过(不影响任务)：%v；%s", t.ID, base, err, clipRecon(out))
		emit("tool_result", "playwright-cli", "浏览器渲染失败，跳过浏览器侦察(不影响任务)", clipRecon(out), true)
		return
	}
	emit("tool_result", "playwright-cli", "目标已在真浏览器中渲染完成", "", false)
	defer func() { _, _ = run("close") }()
	// Let the SPA bootstrap and fire its runtime XHR/fetch before harvesting.
	select {
	case <-ctx.Done():
		return
	case <-time.After(6 * time.Second):
	}

	emit("tool_use", "playwright-cli", "requests（抓运行时 XHR/fetch）+ 抽取前端路由", "", false)
	reqOut, _ := run("--raw", "requests")
	linksOut, _ := run("--raw", "eval", `JSON.stringify([...document.querySelectorAll("a[href]")].map(a=>a.getAttribute("href")))`)

	// --- backend endpoints (same-host, non-static) ---
	seen := map[string]bool{}
	var endpoints []string
	for _, line := range strings.Split(reqOut, "\n") {
		m := reReqLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		method, rawURL := m[1], m[2]
		u, err := url.Parse(rawURL)
		if err != nil || !strings.EqualFold(u.Hostname(), host) || isStaticReconPath(u.Path) {
			continue
		}
		key := method + " " + u.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		endpoints = append(endpoints, key)
		epURL := u.Scheme + "://" + u.Host + u.Path
		if _, err := as.UpsertEndpoint(db.UpsertEndpointReq{URL: epURL, Method: method, TaskID: taskID}); err != nil {
			log.Printf("[recon] task %s 登记端点 %s 失败：%v", t.ID, key, err)
		}
	}

	// --- frontend routes (hash or path links) ---
	var routes []string
	routeSeen := map[string]bool{}
	if arr := parseLinkArray(linksOut); len(arr) > 0 {
		for _, h := range arr {
			h = strings.TrimSpace(h)
			if h == "" || routeSeen[h] {
				continue
			}
			if strings.HasPrefix(h, "#/") || (strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//")) {
				routeSeen[h] = true
				routes = append(routes, h)
			}
		}
	}

	harvestDetail := "运行时接口：\n" + strings.Join(endpoints, "\n")
	if len(routes) > 0 {
		harvestDetail += "\n\n前端路由：\n" + strings.Join(routes, "\n")
	}
	emit("tool_result", "playwright-cli", fmt.Sprintf("解析到 %d 个运行时后端接口、%d 条前端路由", len(endpoints), len(routes)), harvestDetail, false)

	if len(endpoints) == 0 && len(routes) == 0 {
		log.Printf("[recon] task %s 浏览器侦察完成：未解析到接口/路由(可能非 Web 或需登录)", t.ID)
		emit("result", "", "浏览器侦察完成：未解析到接口/路由（可能非 Web、空站或需登录）", "", false)
		return
	}

	summary := fmt.Sprintf("浏览器确定性侦察：渲染 %s，发现 %d 个运行时后端接口、%d 条前端路由（已写入资产图）", base, len(endpoints), len(routes))
	detail := "运行时接口(method path)：\n" + strings.Join(endpoints, "\n")
	if len(routes) > 0 {
		detail += "\n\n前端路由：\n" + strings.Join(routes, "\n")
	}
	payload := map[string]any{
		"summary":    summary,
		"detail":     detail,
		"evidence":   "playwright-cli open --browser=chromium " + base + " → requests / anchors",
		"confidence": "observed",
	}
	if fid, err := t.Store.AddNode(db.KindFact, payload, 5, "confirmed", "recon", nil); err != nil {
		log.Printf("[recon] task %s 写入侦察事实失败：%v", t.ID, err)
	} else if reconIntentID > 0 {
		_ = t.Store.Link(reconIntentID, db.RelYields, fid) // intent → yields → fact
	}
	emit("result", "", summary, detail, false)
	log.Printf("[recon] task %s 浏览器确定性侦察完成：%d 接口 / %d 路由", t.ID, len(endpoints), len(routes))
	t.Notify() // new assets/fact → wake the planner to act on the mapped surface
}

// parseLinkArray decodes the double-encoded JSON that `playwright-cli --raw eval
// "JSON.stringify([...])"` prints: an outer JSON string whose contents are a JSON
// array of hrefs. Tolerates either a quoted string or a bare array.
func parseLinkArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var inner string
	if json.Unmarshal([]byte(raw), &inner) == nil { // outer quoted string
		raw = inner
	}
	var hrefs []string
	if json.Unmarshal([]byte(raw), &hrefs) != nil {
		return nil
	}
	return hrefs
}

// clipRecon trims a playwright-cli error blob for a single log line.
func clipRecon(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

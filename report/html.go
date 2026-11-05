package report

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

// HTML renders a self-contained, print-friendly HTML report (A4), suitable for
// viewing or converting to PDF (e.g. via headless Chromium --print-to-pdf). When
// execSummary is non-empty it is shown as the Executive Summary section.
func HTML(in Input, execSummary string) string {
	views := make([]findingView, 0, len(in.Findings))
	for _, n := range in.Findings {
		views = append(views, parseFinding(n))
	}
	sort.SliceStable(views, func(i, j int) bool { return sevRank[views[i].Severity] < sevRank[views[j].Severity] })

	sevCount := map[string]int{}
	for _, v := range views {
		sev := v.Severity
		if sev == "" {
			sev = "info"
		}
		sevCount[sev]++
	}

	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"zh\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	fmt.Fprintf(&b, "<title>%s · 安全测试报告</title>", html.EscapeString(nz(in.Title, "安全测试报告")))
	b.WriteString(reportCSS)
	b.WriteString("</head><body>")

	fmt.Fprintf(&b, "<h1>%s</h1>", html.EscapeString(nz(in.Title, "安全测试报告")))
	b.WriteString("<div class=\"meta\">")
	fmt.Fprintf(&b, "<div><b>生成时间：</b>%s</div>", in.GeneratedAt.Format(time.RFC3339))
	b.WriteString("</div>")

	// severity summary chips
	b.WriteString("<div class=\"chips\">")
	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		if n := sevCount[sev]; n > 0 {
			fmt.Fprintf(&b, "<span class=\"chip sev-%s\">%s %d</span>", sev, sevLabel(sev), n)
		}
	}
	if len(views) == 0 {
		b.WriteString("<span class=\"chip\">无已确认发现</span>")
	}
	b.WriteString("</div>")

	if strings.TrimSpace(execSummary) != "" {
		b.WriteString("<h2>执行摘要</h2><div class=\"exec\">")
		for _, para := range strings.Split(strings.TrimSpace(execSummary), "\n") {
			if p := strings.TrimSpace(para); p != "" {
				fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(p))
			}
		}
		b.WriteString("</div>")
	}

	if strings.TrimSpace(in.Goal) != "" {
		b.WriteString("<details class=\"scope\"><summary>测试范围与规则</summary>")
		for _, para := range strings.Split(strings.TrimSpace(in.Goal), "\n") {
			if p := strings.TrimSpace(para); p != "" {
				fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(p))
			}
		}
		b.WriteString("</details>")
	}

	if len(in.AssetCounts) > 0 {
		b.WriteString("<h2>资产概览</h2><ul class=\"assets\">")
		for _, ty := range []string{"root_domain", "subdomain", "ip", "app", "service", "endpoint"} {
			if c := in.AssetCounts[ty]; c > 0 {
				fmt.Fprintf(&b, "<li>%s: %d</li>", html.EscapeString(ty), c)
			}
		}
		b.WriteString("</ul>")
	}

	b.WriteString("<h2>发现</h2>")
	if len(views) == 0 {
		b.WriteString("<p class=\"muted\">本次未产生可复现证据的确认发现。</p>")
	}
	for i, v := range views {
		sev := v.Severity
		if sev == "" {
			sev = "info"
		}
		b.WriteString("<div class=\"finding\">")
		fmt.Fprintf(&b, "<h3><span class=\"badge sev-%s\">%s</span> %d. %s</h3>", sev, sevLabel(sev), i+1, html.EscapeString(nz(v.Name, "(未命名)")))
		if v.VulnClass != "" {
			fmt.Fprintf(&b, "<div class=\"cls\">%s</div>", html.EscapeString(v.VulnClass))
		}
		if v.Summary != "" {
			fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(v.Summary))
		}
		if v.PoC != "" {
			fmt.Fprintf(&b, "<div class=\"poc-label\">PoC / 证据</div><pre>%s</pre>", html.EscapeString(v.PoC))
		}
		b.WriteString("</div>")
	}

	b.WriteString("</body></html>")
	return b.String()
}

// Digest renders a compact plain-text list of findings (class · severity · name ·
// summary) for feeding an LLM executive-summary prompt. Empty when no findings.
func Digest(findings []*db.Node) string {
	if len(findings) == 0 {
		return ""
	}
	views := make([]findingView, 0, len(findings))
	for _, n := range findings {
		views = append(views, parseFinding(n))
	}
	sort.SliceStable(views, func(i, j int) bool { return sevRank[views[i].Severity] < sevRank[views[j].Severity] })
	var b strings.Builder
	for i, v := range views {
		sev := v.Severity
		if sev == "" {
			sev = "info"
		}
		fmt.Fprintf(&b, "%d. [%s] %s — %s\n", i+1, sev, nz(v.Name, "(未命名)"), v.Summary)
	}
	return b.String()
}

func sevLabel(sev string) string {
	switch sev {
	case "critical":
		return "严重"
	case "high":
		return "高危"
	case "medium":
		return "中危"
	case "low":
		return "低危"
	default:
		return "信息"
	}
}

const reportCSS = `<style>
:root{--fg:#1a1a1a;--muted:#6b7280;--line:#e5e7eb;--crit:#b91c1c;--high:#ea580c;--med:#ca8a04;--low:#2563eb;--info:#6b7280}
*{box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;color:var(--fg);max-width:860px;margin:0 auto;padding:40px 24px;line-height:1.6}
h1{font-size:26px;margin:0 0 8px;border-bottom:3px solid var(--fg);padding-bottom:10px}
h2{font-size:19px;margin:28px 0 10px;border-bottom:1px solid var(--line);padding-bottom:6px}
h3{font-size:16px;margin:0 0 6px}
.meta{color:var(--muted);font-size:13px;margin-bottom:14px}
.chips{display:flex;gap:8px;flex-wrap:wrap;margin:10px 0}
.chip{border:1px solid var(--line);border-radius:999px;padding:3px 12px;font-size:13px}
.badge{display:inline-block;color:#fff;border-radius:4px;padding:1px 8px;font-size:12px;margin-right:6px;vertical-align:middle}
.sev-critical{background:var(--crit)} .sev-high{background:var(--high)} .sev-medium{background:var(--med)} .sev-low{background:var(--low)} .sev-info{background:var(--info)}
.chip.sev-critical,.chip.sev-high,.chip.sev-medium,.chip.sev-low,.chip.sev-info{color:#fff;border-color:transparent}
.exec p{margin:6px 0}
.assets{columns:2;font-size:14px;color:var(--muted)}
.finding{border:1px solid var(--line);border-radius:8px;padding:14px 16px;margin:12px 0;break-inside:avoid}
.cls{color:var(--muted);font-size:12px;margin-bottom:6px}
.poc-label{font-size:12px;color:var(--muted);margin-top:8px}
pre{background:#f6f8fa;border:1px solid var(--line);border-radius:6px;padding:10px;overflow:auto;font-size:12.5px;white-space:pre-wrap;word-break:break-word}
.muted{color:var(--muted)}
.scope{margin:14px 0;border:1px solid var(--line);border-radius:8px;padding:8px 14px;background:#fafafa}
.scope summary{cursor:pointer;font-weight:500;font-size:14px;color:var(--muted)}
.scope p{margin:6px 0;font-size:13px;color:var(--muted)}
@media print{body{padding:0}a{color:inherit}.scope{background:none}.scope[open] summary{margin-bottom:6px}}
</style>`

package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func findingNode(class, name, sev, summary, poc string) *db.Node {
	p, _ := json.Marshal(map[string]any{
		"vulnclass": class, "name": name, "severity": sev, "summary": summary,
		"evidence": map[string]any{"poc": poc},
	})
	return &db.Node{Kind: db.KindFinding, Payload: p}
}

func TestHTML_RendersFindingsAndSummary(t *testing.T) {
	in := Input{
		Title: "门户测试", Goal: "https://portal.example.com", GeneratedAt: time.Unix(0, 0),
		AssetCounts: map[string]int{"subdomain": 3},
		Findings: []*db.Node{
			findingNode("authz", "越权读取他人订单", "high", "A 账号可读 B 的订单", "GET /orders/123 返回他人数据"),
			findingNode("xss", "反射型 XSS", "low", "搜索参数回显未转义", "?q=<script>alert(1)</script>"),
		},
	}
	out := HTML(in, "整体风险中等，最需关注越权读取。")
	for _, want := range []string{"门户测试", "执行摘要", "整体风险中等", "越权读取他人订单", "反射型 XSS", "GET /orders/123", "高危", "资产概览"} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	// high should sort before low
	if strings.Index(out, "越权读取他人订单") > strings.Index(out, "反射型 XSS") {
		t.Error("findings not sorted by severity (high before low)")
	}
	// PoC must be HTML-escaped (no live script tag)
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("PoC not HTML-escaped")
	}
}

func TestDigest(t *testing.T) {
	d := Digest([]*db.Node{findingNode("ssrf", "SSRF 到元数据", "critical", "可读云元数据", "")})
	if !strings.Contains(d, "critical") || !strings.Contains(d, "SSRF 到元数据") {
		t.Fatalf("digest wrong: %q", d)
	}
	if Digest(nil) != "" {
		t.Fatal("empty findings should yield empty digest")
	}
}

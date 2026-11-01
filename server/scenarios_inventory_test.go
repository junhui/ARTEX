package server

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestParseInventory_ArrayAndEnvelope(t *testing.T) {
	// top-level array, flexible field names
	arr := []byte(`[{"host":"a.example.com","org":"ACME","ip":"1.1.1.1","tags":["web"]}]`)
	got, err := parseInventory(arr)
	if err != nil {
		t.Fatalf("array parse: %v", err)
	}
	if len(got) != 1 || got[0].Domain != "a.example.com" || got[0].Company != "ACME" {
		t.Fatalf("unexpected array parse: %+v", got)
	}
	if len(got[0].IPs) != 1 || got[0].IPs[0] != "1.1.1.1" {
		t.Fatalf("ip not parsed: %+v", got[0])
	}

	// {data:[...]} envelope with array ips
	env := []byte(`{"data":[{"domain":"b.example.com","ips":["2.2.2.2","3.3.3.3"]},{"name":""}]}`)
	got, err = parseInventory(env)
	if err != nil {
		t.Fatalf("envelope parse: %v", err)
	}
	if len(got) != 1 { // blank-domain entry dropped
		t.Fatalf("expected 1 valid domain, got %d: %+v", len(got), got)
	}
	if len(got[0].IPs) != 2 {
		t.Fatalf("expected 2 ips, got %+v", got[0].IPs)
	}
}

func TestMockInventoryProvider(t *testing.T) {
	ds, err := mockInventoryProvider{}.ListDomains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) == 0 {
		t.Fatal("mock provider returned no domains")
	}
	for _, d := range ds {
		if strings.TrimSpace(d.Domain) == "" {
			t.Fatalf("mock domain missing name: %+v", d)
		}
	}
}

func TestComposeScenarioGoal(t *testing.T) {
	tmpls := []*db.TaskTemplate{
		{Name: "Web 常规", Goal: "do web testing", InterceptRules: []db.TaskInterceptRuleInput{
			{Action: "ask", Kind: "host", Pattern: "*.example.com"},
		}},
		{Name: "SSRF", Goal: "do ssrf testing", InterceptRules: []db.TaskInterceptRuleInput{
			{Action: "ask", Kind: "host", Pattern: "*.example.com"}, // dup → unioned away
			{Action: "block", Kind: "host", Pattern: "169.254.169.254"},
		}},
	}
	c := constraintByID("safe_validate")
	goal, rules := composeScenarioGoal("shop.example.com", "focus on checkout", tmpls, c, true)

	for _, want := range []string{"强约束", "严格不改数据", "测试目标：shop.example.com", "域名画像采集", "## 场景：Web 常规", "do web testing", "## 场景：SSRF", "## 额外说明", "focus on checkout"} {
		if !strings.Contains(goal, want) {
			t.Errorf("goal missing %q\n---\n%s", want, goal)
		}
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 unioned rules, got %d: %+v", len(rules), rules)
	}
}

func TestComposeScenarioGoal_ExtraOnly_NoProfile(t *testing.T) {
	goal, rules := composeScenarioGoal("api.example.com", "just poke the login", nil, nil, false)
	if !strings.Contains(goal, "测试目标：api.example.com") || !strings.Contains(goal, "just poke the login") {
		t.Fatalf("extra-only goal wrong: %s", goal)
	}
	if strings.Contains(goal, "域名画像采集") {
		t.Fatalf("profile should be off: %s", goal)
	}
	if rules != nil {
		t.Fatalf("expected no rules, got %+v", rules)
	}
}

func TestConstraintByID(t *testing.T) {
	if constraintByID("") == nil || constraintByID("").ID != "safe_validate" {
		t.Fatal("empty should default to safe_validate")
	}
	if constraintByID("intrusive") == nil {
		t.Fatal("intrusive should resolve")
	}
	if constraintByID("nope") != nil {
		t.Fatal("unknown should be nil")
	}
}

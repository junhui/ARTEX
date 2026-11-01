package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/db"
)

// 域名清单(domain inventory)集成(方案 工作流 C 第二阶段)。
//
// 从 InfoSec Portal 拉取域名清单,映射进 ARTEX 的公司 + 资产库(参照 ScopeSentry 同步),
// 并支持"在清单里点域名 → 选场景 → 一键发起测试"(见 scenarios.go)。
//
// Portal 的清单接口尚未定义 → 默认使用内置 MOCK provider,功能即刻可用;配置
// infosec_portal.inventory.path 后自动切换为真实 Portal(provider 是可替换的接缝)。

// InventoryDomain is one normalized inventory entry.
type InventoryDomain struct {
	Domain  string   `json:"domain"`
	Company string   `json:"company,omitempty"` // owning org / business unit
	IPs     []string `json:"ips,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Note    string   `json:"note,omitempty"`
	Source  string   `json:"source,omitempty"`
}

// InventoryProvider abstracts the domain-inventory data source so the mock and the
// real Portal API are interchangeable.
type InventoryProvider interface {
	Name() string
	ListDomains(ctx context.Context) ([]InventoryDomain, error)
}

// inventoryProvider returns the real Portal provider when inventory is configured,
// otherwise the built-in mock.
func (s *Server) inventoryProvider() InventoryProvider {
	cfg := config.InfoSecPortalConfig()
	if strings.TrimSpace(cfg.BaseURL) != "" && strings.TrimSpace(cfg.Inventory.Path) != "" {
		return &portalInventoryProvider{s: s, cfg: cfg}
	}
	return mockInventoryProvider{}
}

// ---------- mock provider ----------

type mockInventoryProvider struct{}

func (mockInventoryProvider) Name() string { return "mock" }

func (mockInventoryProvider) ListDomains(context.Context) ([]InventoryDomain, error) {
	return []InventoryDomain{
		{Domain: "portal.corp.example.com", Company: "Example Corp", IPs: []string{"10.0.0.11"}, Tags: []string{"internal", "sso"}, Note: "员工门户(SSO)", Source: "mock"},
		{Domain: "api.corp.example.com", Company: "Example Corp", IPs: []string{"10.0.0.12"}, Tags: []string{"api"}, Note: "内部 API 网关", Source: "mock"},
		{Domain: "shop.example.com", Company: "Example Retail", IPs: []string{"203.0.113.20"}, Tags: []string{"public", "web"}, Note: "电商前台", Source: "mock"},
		{Domain: "admin.example.com", Company: "Example Retail", IPs: []string{"203.0.113.21"}, Tags: []string{"admin"}, Note: "后台管理", Source: "mock"},
		{Domain: "hr.internal.example.com", Company: "Example HR", IPs: []string{"10.1.0.30"}, Tags: []string{"internal", "sso"}, Note: "人力系统(SSO)", Source: "mock"},
	}, nil
}

// ---------- real Portal provider ----------

type portalInventoryProvider struct {
	s   *Server
	cfg config.InfoSecPortal
}

func (p *portalInventoryProvider) Name() string { return "infosec-portal" }

func (p *portalInventoryProvider) ListDomains(ctx context.Context) ([]InventoryDomain, error) {
	body, status, err := p.s.portalGET(ctx, p.cfg, p.cfg.Inventory.Method, p.cfg.Inventory.Path)
	if err != nil {
		return nil, err
	}
	if status != 0 && (status < 200 || status >= 300) {
		return nil, fmt.Errorf("Portal 清单接口返回 HTTP %d", status)
	}
	return parseInventory(body)
}

// parseInventory leniently extracts domains from an undefined-but-reasonable JSON
// shape: a top-level array, or an object with one of data/domains/result/items as
// the list. Per-item field names are matched flexibly so the real endpoint fits by
// configuration alone.
func parseInventory(body []byte) ([]InventoryDomain, error) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil, nil
	}
	var items []map[string]any
	if body[0] == '[' {
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("解析清单数组失败: %w", err)
		}
	} else {
		var env map[string]json.RawMessage
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("解析清单对象失败: %w", err)
		}
		for _, key := range []string{"data", "domains", "result", "items", "list"} {
			if raw, ok := env[key]; ok && len(raw) > 0 && raw[0] == '[' {
				_ = json.Unmarshal(raw, &items)
				break
			}
		}
	}
	out := make([]InventoryDomain, 0, len(items))
	for _, it := range items {
		d := InventoryDomain{
			Domain:  firstStr(it, "domain", "host", "name", "fqdn"),
			Company: firstStr(it, "company", "org", "organization", "owner", "business_unit"),
			Note:    firstStr(it, "note", "description", "remark"),
			Source:  "infosec-portal",
		}
		d.IPs = firstStrSlice(it, "ips", "ip", "addresses")
		d.Tags = firstStrSlice(it, "tags", "labels")
		if strings.TrimSpace(d.Domain) != "" {
			out = append(out, d)
		}
	}
	return out, nil
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if sv, ok := v.(string); ok && strings.TrimSpace(sv) != "" {
				return strings.TrimSpace(sv)
			}
		}
	}
	return ""
}

func firstStrSlice(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				return []string{strings.TrimSpace(x)}
			}
		case []any:
			var out []string
			for _, e := range x {
				if sv, ok := e.(string); ok && strings.TrimSpace(sv) != "" {
					out = append(out, strings.TrimSpace(sv))
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

// ---------- HTTP: GET /api/inventory/domains ----------

func (s *Server) inventoryDomains(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	p := s.inventoryProvider()
	domains, err := p.ListDomains(ctx)
	if err != nil {
		writeErr(w, 502, "拉取域名清单失败: "+err.Error())
		return
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Domain < domains[j].Domain })
	writeJSON(w, 200, map[string]any{"source": p.Name(), "domains": domains})
}

// ---------- HTTP: POST /api/inventory/sync ----------

type inventorySyncReq struct {
	Domains       []string `json:"domains"`        // optional subset; empty = sync all
	CreateCompany bool     `json:"create_company"` // also upsert owning companies + scope
}

func (s *Server) inventorySync(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	as := s.assetStore()
	cs := s.companyStore()
	if as == nil || cs == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	var req inventorySyncReq
	_ = decode(r, &req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	p := s.inventoryProvider()
	all, err := p.ListDomains(ctx)
	if err != nil {
		writeErr(w, 502, "拉取域名清单失败: "+err.Error())
		return
	}

	want := map[string]bool{}
	for _, d := range req.Domains {
		if t := strings.TrimSpace(strings.ToLower(d)); t != "" {
			want[t] = true
		}
	}

	synced := map[string]int{"domain": 0, "ip": 0}
	companySet := map[string]bool{}
	var warnings []string
	madeCompany := false

	for _, d := range all {
		if len(want) > 0 && !want[strings.ToLower(d.Domain)] {
			continue
		}
		if req.CreateCompany && strings.TrimSpace(d.Company) != "" {
			cid, cerr := cs.UpsertByName(d.Company)
			if cerr != nil {
				warnings = append(warnings, fmt.Sprintf("公司 %s 创建失败: %v", d.Company, cerr))
			} else {
				companySet[d.Company] = true
				madeCompany = true
				cs.AddScope(cid, []string{d.Domain}, "infosec-portal:"+d.Company)
			}
		}
		if _, e := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: d.Domain}); e != nil {
			warnings = append(warnings, fmt.Sprintf("域名 %s 入库失败: %v", d.Domain, e))
			continue
		}
		synced["domain"]++
		for _, ip := range d.IPs {
			if strings.TrimSpace(ip) == "" {
				continue
			}
			if _, e := as.UpsertIP(db.UpsertIPReq{IP: ip, BoundDomains: []string{d.Domain}}); e == nil {
				synced["ip"]++
			}
		}
	}
	if madeCompany {
		_ = cs.RecomputeAttribution()
	}

	companies := make([]string, 0, len(companySet))
	for c := range companySet {
		companies = append(companies, c)
	}
	sort.Strings(companies)
	writeJSON(w, 200, map[string]any{
		"source":    p.Name(),
		"synced":    synced,
		"companies": companies,
		"warnings":  warnings,
	})
}

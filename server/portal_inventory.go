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
	pg := s.pg(w)
	if pg == nil {
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
	// Merge locally, manually-added domains (source="manual") into the provider list,
	// deduped by domain so a manual entry never masks a provider one.
	if manual, _ := loadManualInventory(pg); len(manual) > 0 {
		seen := map[string]bool{}
		for _, d := range domains {
			seen[strings.ToLower(d.Domain)] = true
		}
		for _, m := range manual {
			if !seen[strings.ToLower(m.Domain)] {
				domains = append(domains, m)
			}
		}
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Domain < domains[j].Domain })
	writeJSON(w, 200, map[string]any{"source": p.Name(), "domains": domains})
}

// ---------- manual inventory (locally added, merged into the listing) ----------

// settingManualInventory is the KV-setting key holding the JSON array of
// manually-added inventory domains. Stored as a setting so no schema migration is
// needed; merged into the provider listing by inventoryDomains.
const settingManualInventory = "inventory_manual_domains"

func loadManualInventory(pg *db.DB) ([]InventoryDomain, error) {
	raw, ok, err := pg.GetSetting(settingManualInventory)
	if err != nil || !ok || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var list []InventoryDomain
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil, nil // corrupt value → treat as empty rather than failing the page
	}
	return list, nil
}

func saveManualInventory(pg *db.DB, list []InventoryDomain) error {
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return pg.SetSetting(settingManualInventory, string(b))
}

// normalizeInventoryDomain extracts a bare host from user input: strips a scheme,
// any path, and a trailing :port; lowercases. Returns "" if nothing usable remains.
func normalizeInventoryDomain(in string) string {
	v := strings.TrimSpace(strings.ToLower(in))
	if v == "" {
		return ""
	}
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	// strip :port (but keep IPv6 literals intact; inventory domains are plain hosts)
	if !strings.Contains(v, "]") {
		if i := strings.LastIndex(v, ":"); i >= 0 {
			if _, _, ok := splitHostMaybePort(v, i); ok {
				v = v[:i]
			}
		}
	}
	return strings.TrimSpace(v)
}

// splitHostMaybePort reports whether the part after the last colon looks like a port.
func splitHostMaybePort(v string, i int) (host, port string, isPort bool) {
	port = v[i+1:]
	if port == "" {
		return v[:i], "", false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return v[:i], port, false
		}
	}
	return v[:i], port, true
}

type inventoryAddReq struct {
	Domain  string   `json:"domain"`
	Company string   `json:"company"`
	IPs     []string `json:"ips"`
	Tags    []string `json:"tags"`
	Note    string   `json:"note"`
}

// inventoryAddDomain adds one manually-entered domain to the local inventory list.
// POST /api/inventory/domains
func (s *Server) inventoryAddDomain(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req inventoryAddReq
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	dom := normalizeInventoryDomain(req.Domain)
	if dom == "" {
		writeErr(w, 400, "域名不能为空")
		return
	}
	list, _ := loadManualInventory(pg)
	for _, d := range list {
		if strings.EqualFold(d.Domain, dom) {
			writeErr(w, 409, "该域名已在手动清单中")
			return
		}
	}
	clean := func(ss []string) []string {
		out := make([]string, 0, len(ss))
		for _, s := range ss {
			if t := strings.TrimSpace(s); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	list = append(list, InventoryDomain{
		Domain:  dom,
		Company: strings.TrimSpace(req.Company),
		IPs:     clean(req.IPs),
		Tags:    clean(req.Tags),
		Note:    strings.TrimSpace(req.Note),
		Source:  "manual",
	})
	if err := saveManualInventory(pg, list); err != nil {
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "domain": dom})
}

// inventoryDeleteDomain removes one manually-added domain. Only entries added
// manually can be removed here (provider domains are not stored locally).
// DELETE /api/inventory/domains?domain=<d>
func (s *Server) inventoryDeleteDomain(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	dom := normalizeInventoryDomain(r.URL.Query().Get("domain"))
	if dom == "" {
		writeErr(w, 400, "domain 不能为空")
		return
	}
	list, _ := loadManualInventory(pg)
	kept := make([]InventoryDomain, 0, len(list))
	removed := false
	for _, d := range list {
		if strings.EqualFold(d.Domain, dom) {
			removed = true
			continue
		}
		kept = append(kept, d)
	}
	if !removed {
		writeErr(w, 404, "该域名不在手动清单中(来自同步源的域名无法在此删除)")
		return
	}
	if err := saveManualInventory(pg, kept); err != nil {
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": dom})
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

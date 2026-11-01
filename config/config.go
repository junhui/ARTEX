// Package config loads runtime configuration from a JSON file, with environment
// variables taking precedence. Currently it carries the PostgreSQL connection.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Database is the PostgreSQL connection config. Either set DSN directly, or set
// the component fields and a DSN is assembled from them.
type Database struct {
	DSN      string `json:"dsn"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

// Config is the on-disk config file shape.
type Config struct {
	Database      Database      `json:"database"`
	SkillDir      string        `json:"skill_dir"`
	InfoSecPortal InfoSecPortal `json:"infosec_portal"`
	BrowserLogin  BrowserLogin  `json:"browser_login"`
}

// BrowserLogin configures the OPTIONAL human-in-the-loop SSO login handoff: a
// human completes SSO (incl. MFA) in a server-side headful browser viewed over
// noVNC, then the agent reuses the authenticated session. When Enabled is false
// (default) the feature is entirely off — the browser_login tool is not offered
// and no VNC/Chromium processes are expected, so ARTEX is unaffected.
//
// The VNC stack (Xvfb + x11vnc + noVNC) and the headful Chromium (with the CDP
// debugging port) are started by the container/start script when enabled; ARTEX
// only drives that Chromium over CDP. See design/sso-login.md.
type BrowserLogin struct {
	Enabled      bool   `json:"enabled"`        // master switch (default off)
	NoVNCURL     string `json:"novnc_url"`      // operator-facing noVNC page URL (e.g. http://host:6080/vnc.html?autoconnect=1)
	CDPEndpoint  string `json:"cdp_endpoint"`   // Chromium CDP endpoint ARTEX drives (default http://127.0.0.1:9222)
	NodeHelper   string `json:"node_helper"`    // path to the CDP helper script (default skills/authenticated-session/scripts/cdp-helper.js)
	TimeoutSec   int    `json:"timeout_sec"`    // max seconds to wait for the human (default 600)
	StateSubpath string `json:"state_subpath"`  // storage-state filename under the task dir (default auth-state.json)
}

// BrowserLoginConfig resolves the browser-login config with environment overrides:
//
//	env ARTEX_BROWSER_LOGIN=1          force-enables the feature
//	env ARTEX_BROWSER_LOGIN_NOVNC_URL  overrides the operator-facing noVNC URL
//	env ARTEX_BROWSER_LOGIN_CDP        overrides the CDP endpoint
func BrowserLoginConfig() BrowserLogin {
	b := Load().BrowserLogin
	if v := strings.TrimSpace(os.Getenv("ARTEX_BROWSER_LOGIN")); v == "1" || strings.EqualFold(v, "true") {
		b.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("ARTEX_BROWSER_LOGIN_NOVNC_URL")); v != "" {
		b.NoVNCURL = v
	}
	if v := strings.TrimSpace(os.Getenv("ARTEX_BROWSER_LOGIN_CDP")); v != "" {
		b.CDPEndpoint = v
	}
	if strings.TrimSpace(b.CDPEndpoint) == "" {
		b.CDPEndpoint = "http://127.0.0.1:9222"
	}
	if strings.TrimSpace(b.NoVNCURL) == "" {
		b.NoVNCURL = "/novnc/vnc.html?autoconnect=1&resize=remote"
	}
	if b.TimeoutSec <= 0 {
		b.TimeoutSec = 600
	}
	if strings.TrimSpace(b.StateSubpath) == "" {
		b.StateSubpath = "auth-state.json"
	}
	return b
}

// InfoSecPortalTool is one HTTP-backed tool the InfoSec Portal exposes to agents
// (e.g. "look up an IP", "look up an asset"). Each becomes a callable agent tool
// that issues one HTTP request to the portal. Path/Query/Headers/Body may embed
// {param} placeholders that are filled from the tool call's arguments at runtime.
type InfoSecPortalTool struct {
	Key         string            `json:"key"`         // tool name (lowercase, [a-z0-9_]); must be unique
	Description string            `json:"description"` // shown to the model: what it does + when to use it
	Method      string            `json:"method"`      // HTTP method (default GET)
	Path        string            `json:"path"`        // appended to base_url; may contain {param}
	Query       map[string]string `json:"query"`       // query params; values may contain {param}
	Headers     map[string]string `json:"headers"`     // per-tool headers; values may contain {param}
	Body        string            `json:"body"`        // request body; may contain {param}
	Schema      json.RawMessage   `json:"schema"`      // JSON Schema for the tool's arguments
	Agents      []string          `json:"agents"`      // per-tool binding override (falls back to portal-level agents)
	Enabled     *bool             `json:"enabled"`     // nil/true = on; false = skip this tool
}

// InfoSecPortal is the OPTIONAL internal InfoSec Portal integration. It is purely
// additive: when base_url is empty (the section omitted), NO portal tools are
// registered and ARTEX behaves exactly as if the feature did not exist — the only
// effect of leaving it unconfigured is that those lookup tools are unavailable.
// ARTEX stays an independent project; the portal is referenced over plain HTTP.
type InfoSecPortal struct {
	BaseURL           string              `json:"base_url"`            // e.g. https://infosec.internal (empty = feature off)
	Headers           map[string]string   `json:"headers"`             // static headers on every call (e.g. auth)
	Insecure          bool                `json:"insecure"`            // skip TLS verification (self-signed internal certs)
	Proxy             string              `json:"proxy"`               // optional egress proxy for portal calls
	UseRecordingProxy bool                `json:"use_recording_proxy"` // route through ARTEX's recording proxy (default false)
	TimeoutMs         int                    `json:"timeout_ms"` // per-call timeout (default 30000)
	Agents            []string               `json:"agents"`     // default agent binding for all tools (fallback: worker/planner/auto)
	Tools             []InfoSecPortalTool    `json:"tools"`      // the tools to expose
	Inventory         InfoSecPortalInventory `json:"inventory"` // optional domain-inventory source
}

// InfoSecPortalInventory points at the Portal endpoint that lists the domain
// inventory. When Path is empty, ARTEX falls back to a built-in MOCK provider, so
// the inventory/scenario features work before the real Portal API exists. The
// response parser is intentionally lenient (top-level array, or {data|domains|
// result|items:[...]}, with flexible per-item field names) so the real endpoint
// slots in by configuration alone.
type InfoSecPortalInventory struct {
	Path   string `json:"path"`   // inventory list path (relative to base_url); empty = use mock
	Method string `json:"method"` // HTTP method (default GET)
}

// InfoSecPortalConfig resolves the portal config with environment overrides, so
// secrets (tokens) and the base URL can be injected without editing the file:
//
//	env ARTEX_INFOSEC_PORTAL_BASE_URL  overrides base_url
//	env ARTEX_INFOSEC_PORTAL_TOKEN     sets the "Authorization: Bearer <token>" header
//
// A missing config file or absent section yields a zero value (feature off).
func InfoSecPortalConfig() InfoSecPortal {
	p := Load().InfoSecPortal
	if v := strings.TrimSpace(os.Getenv("ARTEX_INFOSEC_PORTAL_BASE_URL")); v != "" {
		p.BaseURL = v
	}
	if tok := strings.TrimSpace(os.Getenv("ARTEX_INFOSEC_PORTAL_TOKEN")); tok != "" {
		if p.Headers == nil {
			p.Headers = map[string]string{}
		}
		p.Headers["Authorization"] = "Bearer " + tok
	}
	return p
}

// BaseDir is the directory that anchors all runtime artifacts (config.json and
// the data/ store). It is the directory the running binary lives in, so a
// distributed executable keeps its files next to itself on any OS (Windows,
// Linux, …) regardless of the working directory it is launched from.
//
// When launched via `go run`, the binary is throwaway: it sits either in a temp
// build dir (cache miss → fresh link) OR straight inside the Go build cache
// (cache hit → run from $GOCACHE/.../...-d). We detect both and fall back to the
// current working directory so dev artifacts (data/, transcripts) and config
// resolve against the project dir, not the throwaway binary's location.
func BaseDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if isGoRunDir(dir) {
		return "." // throwaway `go run` binary → use CWD
	}
	return dir
}

// isGoRunDir reports whether dir is where `go run` parked its executable: under
// the system temp dir (cache miss), or anywhere inside a Go build cache
// (…/go-build/…, the cache-hit case — NOT under os.TempDir(), which is why the
// old temp-only check failed intermittently). In both cases the binary is
// throwaway, so config/data must resolve against the CWD.
func isGoRunDir(dir string) bool {
	if tmp := os.TempDir(); tmp != "" {
		if rel, err := filepath.Rel(tmp, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
		if seg == "go-build" {
			return true
		}
	}
	return false
}

// Path returns the config file path. Resolution order:
//  1. env ARTEX_CONFIG (explicit override)
//  2. ./config.json in the current working directory (running from the project
//     dir — robust no matter where `go run` placed the temp/cached binary)
//  3. config.json next to the executable (a distributed binary keeps it beside)
//
// The first existing file wins. If none exist, the CWD path is returned so the
// "not found" message points at the project dir the user most likely expected.
func Path() string {
	if v := strings.TrimSpace(os.Getenv("ARTEX_CONFIG")); v != "" {
		return v
	}
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "config.json"))
	}
	candidates = append(candidates, filepath.Join(BaseDir(), "config.json"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// Load reads and parses the config file. A missing/unreadable file yields a zero
// Config (so callers fall back to defaults) rather than an error.
func Load() Config {
	var c Config
	b, err := os.ReadFile(Path())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c)
	return c
}

// SkillDir returns the skill root directory with precedence:
//
//	env ARTEX_SKILL_DIR  >  config file (skill_dir)  >  BaseDir()/skills
//
// The directory is created if it does not exist.
func SkillDir() string {
	var d string
	if v := strings.TrimSpace(os.Getenv("ARTEX_SKILL_DIR")); v != "" {
		d = v
	} else if v := strings.TrimSpace(Load().SkillDir); v != "" {
		d = v
	} else {
		d = filepath.Join(BaseDir(), "skills")
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

// PostgresDSN resolves the connection string with precedence:
//
//	env ARTEX_PG_DSN  >  config file (database.dsn, or assembled from fields)
//
// There is NO built-in fallback: when neither source supplies a database config,
// it returns an error naming the config path it inspected, so startup fails loudly
// instead of silently connecting to a wrong default. source describes where the
// DSN came from (for startup logging).
func PostgresDSN() (dsn, source string, err error) {
	if v := strings.TrimSpace(os.Getenv("ARTEX_PG_DSN")); v != "" {
		return v, "环境变量 ARTEX_PG_DSN", nil
	}
	db := Load().Database
	if d := strings.TrimSpace(db.DSN); d != "" {
		return d, "配置文件 " + Path() + " (database.dsn)", nil
	}
	if db.Host != "" || db.DBName != "" || db.User != "" {
		return db.buildDSN(), "配置文件 " + Path() + " (database 字段)", nil
	}
	return "", "", fmt.Errorf("未找到数据库配置：环境变量 ARTEX_PG_DSN 未设置，且配置文件 %s 未提供 database（dsn 或 host/user/dbname）。请创建该配置文件或设置环境变量后重试", Path())
}

func (d Database) buildDSN() string {
	host := d.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := d.Port
	if port == 0 {
		port = 5432
	}
	ssl := d.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   host + ":" + strconv.Itoa(port),
		Path:   "/" + d.DBName,
	}
	if d.User != "" {
		if d.Password != "" {
			u.User = url.UserPassword(d.User, d.Password)
		} else {
			u.User = url.User(d.User)
		}
	}
	u.RawQuery = url.Values{"sslmode": {ssl}}.Encode()
	return u.String()
}

// String is a redacted view of the resolved DSN (password masked) for logging.
func Redact(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	if u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			u.User = url.UserPassword(u.User.Username(), "****")
		}
	}
	return fmt.Sprintf("%s", u.String())
}

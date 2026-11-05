package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/config"
)

// TestInfoSecPortalTool_BuildAndCall verifies a configured portal tool issues the
// right HTTP request: base_url + path with {param} filled, query appended, and the
// portal-wide + per-tool headers set.
func TestInfoSecPortalTool_BuildAndCall(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotPerTool string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotPerTool = r.Header.Get("X-Tool")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ip":"1.2.3.4","owner":"acme"}`))
	}))
	defer srv.Close()

	cfg := config.InfoSecPortal{
		BaseURL: srv.URL,
		Headers: map[string]string{"Authorization": "Bearer t0k"},
		Tools: []config.InfoSecPortalTool{{
			Key:         "portal_ip_lookup",
			Description: "look up an IP",
			Method:      "GET",
			Path:        "/ip/{ip}",
			Query:       map[string]string{"detail": "{detail}"},
			Headers:     map[string]string{"X-Tool": "iplookup"},
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"ip":{"type":"string"},"detail":{"type":"string"}},"required":["ip"]}`),
		}},
	}

	s := &Server{}
	tools := s.infosecPortalToolsFrom(cfg)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name() != "portal_ip_lookup" {
		t.Fatalf("unexpected tool name %q", tools[0].Name())
	}

	in, _ := json.Marshal(map[string]any{"ip": "1.2.3.4", "detail": "full"})
	res, err := tools[0].Call(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error result: %s", res.Flatten())
	}
	if gotPath != "/ip/1.2.3.4" {
		t.Errorf("path = %q, want /ip/1.2.3.4", gotPath)
	}
	if gotQuery != "detail=full" {
		t.Errorf("query = %q, want detail=full", gotQuery)
	}
	if gotAuth != "Bearer t0k" {
		t.Errorf("auth header = %q, want Bearer t0k", gotAuth)
	}
	if gotPerTool != "iplookup" {
		t.Errorf("per-tool header = %q, want iplookup", gotPerTool)
	}
	if out := res.Flatten(); !strings.Contains(out, "acme") {
		t.Errorf("response body not surfaced: %s", out)
	}
}

// TestInfoSecPortalTool_Disabled ensures the feature is inert without config and
// that an explicitly disabled tool is skipped.
func TestInfoSecPortalTool_Disabled(t *testing.T) {
	s := &Server{}
	if got := s.infosecPortalToolsFrom(config.InfoSecPortal{}); got != nil {
		t.Fatalf("no base_url should yield nil tools, got %d", len(got))
	}
	off := false
	cfg := config.InfoSecPortal{
		BaseURL: "https://portal.internal",
		Tools:   []config.InfoSecPortalTool{{Key: "x", Enabled: &off}},
	}
	if got := s.infosecPortalToolsFrom(cfg); len(got) != 0 {
		t.Fatalf("disabled tool should be skipped, got %d", len(got))
	}
}

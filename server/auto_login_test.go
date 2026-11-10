package server

import (
	"testing"
	"time"

	"github.com/Autumn-27/artex/config"
)

func TestTOTPNow_RFC6238Vector(t *testing.T) {
	// RFC 6238 test secret "12345678901234567890" (base32) at T=59s → 6-digit 287082.
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	got := totpNow(secret, time.Unix(59, 0))
	if got != "287082" {
		t.Fatalf("totp = %q, want 287082", got)
	}
	if totpNow("", time.Now()) != "" {
		t.Fatal("empty secret should yield empty totp")
	}
}

func TestMatchAuthProfile(t *testing.T) {
	ps := []config.AuthProfile{
		{Name: "a", Match: "portal.corp.example.com"},
		{Name: "b", Match: "shop.example.com"},
	}
	if p := matchAuthProfile(ps, "https://portal.corp.example.com/login"); p == nil || p.Name != "a" {
		t.Fatalf("expected profile a, got %+v", p)
	}
	if p := matchAuthProfile(ps, "https://unknown.example.org"); p != nil {
		t.Fatalf("expected no match, got %+v", p)
	}
}

func TestResolveAuthSteps(t *testing.T) {
	p := &config.AuthProfile{
		Username:   "alice",
		Password:   "s3cret",
		TOTPSecret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		Steps: []config.AuthStep{
			{Action: "fill", Selector: "#u", Value: "$username"},
			{Action: "fill", Selector: "#p", Value: "$password"},
			{Action: "fill", Selector: "#otp", Value: "$totp"},
		},
	}
	steps := resolveAuthSteps(p, time.Unix(59, 0))
	if steps[0].Value != "alice" || steps[1].Value != "s3cret" || steps[2].Value != "287082" {
		t.Fatalf("resolution wrong: %+v", steps)
	}
}

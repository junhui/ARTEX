package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBrowserLoginManager_RegisterResolve(t *testing.T) {
	m := newBrowserLoginManager()
	p := &pendingLogin{TaskID: "7", LoginURL: "https://x/login", CreatedAt: time.Now()}
	m.register(p)

	if got := m.get("7"); got == nil || got.LoginURL != "https://x/login" {
		t.Fatalf("get after register: %+v", got)
	}
	if len(m.list()) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(m.list()))
	}

	// resolve(done) should signal the waiter and remove the entry.
	go func() { _ = m.resolve("7", true) }()
	select {
	case oc := <-p.ch:
		if !oc.done {
			t.Fatal("expected done=true outcome")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not signaled")
	}
	if m.get("7") != nil {
		t.Fatal("entry should be removed after resolve")
	}
	if m.resolve("7", true) {
		t.Fatal("second resolve should report no pending")
	}
}

func TestBrowserLoginManager_RegisterReplacesOld(t *testing.T) {
	m := newBrowserLoginManager()
	old := &pendingLogin{TaskID: "1", CreatedAt: time.Now()}
	m.register(old)
	// Re-register for same task → old waiter gets cancelled.
	m.register(&pendingLogin{TaskID: "1", CreatedAt: time.Now()})
	select {
	case oc := <-old.ch:
		if oc.done {
			t.Fatal("old waiter should be cancelled (done=false)")
		}
	case <-time.After(time.Second):
		t.Fatal("old waiter not cancelled on re-register")
	}
}

func TestRunBrowserLogin_DisabledByDefault(t *testing.T) {
	s := &Server{}
	res, err := s.runBrowserLogin(context.Background(), json.RawMessage(`{"login_url":"https://x/login"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected disabled-feature error result, got: %s", res.Flatten())
	}
}

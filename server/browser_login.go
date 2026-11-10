package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/config"
	actool "github.com/Autumn-27/norma/tool"
)

// SSO 登录接管(方案 工作流 B)。
//
// 很多内部系统需要 SSO 登录(可能含 MFA),无法用静态凭据自动登录。本模块实现
// "人工登录接管":Agent 调用阻塞式 browser_login 工具 → 后端在服务端内置(有头)
// 浏览器打开登录页 → 人工经 noVNC 完成登录 → 点"我已登录完成" → 后端经 CDP 导出
// 已认证会话态(storage-state)到任务目录 → Agent 用 playwright-cli 复用该会话继续测试。
//
// 可选且不影响现有功能:browser_login.enabled=false(默认)时不提供该工具、不期望任何
// VNC/浏览器进程。VNC 栈(Xvfb+x11vnc+noVNC)与有头 Chromium(带 CDP 端口)由容器/启动
// 脚本在开启时拉起;ARTEX 只经 CDP 驱动该浏览器。详见 design/sso-login.md。

// pendingLogin is one in-flight login handoff, keyed by task id.
type pendingLogin struct {
	TaskID    string    `json:"task_id"`
	LoginURL  string    `json:"login_url"`
	NoVNCURL  string    `json:"novnc_url"`
	CreatedAt time.Time `json:"created_at"`
	ch        chan loginOutcome
}

type loginOutcome struct {
	done bool // true = operator finished login; false = cancelled
}

type browserLoginManager struct {
	mu      sync.Mutex
	pending map[string]*pendingLogin
}

func newBrowserLoginManager() *browserLoginManager {
	return &browserLoginManager{pending: map[string]*pendingLogin{}}
}

func (s *Server) browserLoginMgr() *browserLoginManager {
	s.blOnce.Do(func() { s.blMgr = newBrowserLoginManager() })
	return s.blMgr
}

// register creates (or replaces) a pending handoff for a task and returns it. A
// pre-existing one for the same task is resolved as cancelled so its waiter exits.
func (m *browserLoginManager) register(p *pendingLogin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.pending[p.TaskID]; ok {
		select {
		case old.ch <- loginOutcome{done: false}:
		default:
		}
	}
	p.ch = make(chan loginOutcome, 1)
	m.pending[p.TaskID] = p
}

func (m *browserLoginManager) get(taskID string) *pendingLogin {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[taskID]
}

func (m *browserLoginManager) list() []*pendingLogin {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*pendingLogin, 0, len(m.pending))
	for _, p := range m.pending {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// resolve signals the waiter for a task and removes the pending entry. Returns
// false when there was no pending handoff for that task.
func (m *browserLoginManager) resolve(taskID string, done bool) bool {
	m.mu.Lock()
	p, ok := m.pending[taskID]
	if ok {
		delete(m.pending, taskID)
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case p.ch <- loginOutcome{done: done}:
	default:
	}
	return true
}

// ---------- the blocking browser_login tool ----------

func (s *Server) browserLoginTool() actool.CoreTool {
	return wrTool("browser_login",
		"人工登录接管(SSO/MFA):在服务端内置浏览器打开 login_url 并【阻塞等待人工】经 noVNC 完成登录,"+
			"登录完成后导出已认证会话态(storage-state)并返回其路径,供后续 playwright-cli state-load 复用。"+
			"仅用于已授权目标;需要登录且无法静态凭据自动登录时用它,不要向用户索要明文账号密码。",
		objSchema(map[string]any{
			"login_url":    strParam("登录入口 URL(将在服务端内置浏览器中打开)"),
			"success_hint": strParam("可选:登录成功的判据(登录后 URL 片段或页面元素文案),仅作提示"),
		}, "login_url"),
		s.runBrowserLogin,
	)
}

func (s *Server) runBrowserLogin(ctx context.Context, in json.RawMessage) (actool.Result, error) {
	cfg := config.BrowserLoginConfig()
	if !cfg.Enabled {
		return actool.Errorf("未启用服务端登录接管(browser_login.enabled=false);请在部署中开启,或改用其他已授权访问方式"), nil
	}
	var args struct {
		LoginURL    string `json:"login_url"`
		SuccessHint string `json:"success_hint"`
	}
	_ = json.Unmarshal(in, &args)
	args.LoginURL = strings.TrimSpace(args.LoginURL)
	if args.LoginURL == "" {
		return actool.Errorf("login_url 为必填"), nil
	}
	ri := agent.RunInfoFrom(ctx)
	if ri.TaskID == 0 {
		return actool.Errorf("browser_login 仅在任务上下文中可用"), nil
	}
	taskID := strconv.FormatInt(ri.TaskID, 10)

	// Best-effort: point the server-side browser at the login page. If this fails
	// (helper/endpoint not ready) we still proceed — the operator can navigate in noVNC.
	if out, err := s.cdpHelper(ctx, cfg, "navigate", args.LoginURL); err != nil {
		// non-fatal
		_ = out
	}

	p := &pendingLogin{TaskID: taskID, LoginURL: args.LoginURL, NoVNCURL: cfg.NoVNCURL, CreatedAt: time.Now()}
	mgr := s.browserLoginMgr()
	mgr.register(p)
	defer mgr.resolve(taskID, false) // ensure cleanup if we exit early

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return actool.Errorf("登录接管被取消(任务中断)"), nil
	case <-timer.C:
		return actool.Errorf(fmt.Sprintf("登录接管超时(%ds 内未完成),可重试 browser_login", cfg.TimeoutSec)), nil
	case oc := <-p.ch:
		if !oc.done {
			return actool.Errorf("登录接管已取消"), nil
		}
	}

	// Operator finished — export the authenticated storage-state to the task dir.
	taskDir := filepath.Join(s.m.dir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return actool.Errorf("创建任务目录失败: " + err.Error()), nil
	}
	statePath := filepath.Join(taskDir, cfg.StateSubpath)
	if out, err := s.cdpHelper(ctx, cfg, "export-state", statePath); err != nil {
		return actool.Errorf("导出会话态失败: " + err.Error() + "\n" + out), nil
	}
	rel := filepath.Join("tasks", taskID, cfg.StateSubpath)
	msg := fmt.Sprintf("登录完成。已保存认证会话态到: %s\n后续用 playwright-cli 复用:\n  playwright-cli state-load %s\n  playwright-cli open <登录后页面>",
		statePath, rel)
	return actool.Text(msg), nil
}

// cdpHelper runs the Node CDP helper for one action against the configured CDP
// endpoint, returning its combined output.
func (s *Server) cdpHelper(ctx context.Context, cfg config.BrowserLogin, action, arg string) (string, error) {
	helper := strings.TrimSpace(cfg.NodeHelper)
	if helper == "" {
		helper = filepath.Join(s.skillDir, "authenticated-session", "scripts", "cdp-helper.js")
	}
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	c := exec.CommandContext(runCtx, "node", helper, action, arg)
	c.Env = append(os.Environ(), "CDP_ENDPOINT="+cfg.CDPEndpoint)
	out, err := c.CombinedOutput()
	return string(out), err
}

// seedBrowserLoginTool seeds the tools-table row (bound to worker/auto) when the
// feature is enabled, so its per-agent binding is editable in the UI. No-op when
// disabled; a stale row left after disabling is inert (the in-memory tool is gone).
func (s *Server) seedBrowserLoginTool() {
	if !config.BrowserLoginConfig().Enabled || s.m == nil || s.m.pg == nil {
		return
	}
	t := s.browserLoginTool()
	schema, _ := json.Marshal(t.InputSchema())
	agents, _ := json.Marshal([]string{"worker", "auto"})
	if err := s.m.pg.SeedTool("browser_login", t.Description(), schema, agents); err != nil {
		// best-effort
		_ = err
	}
}

// ---------- HTTP API ----------

func (s *Server) browserLoginPending(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	writeJSON(w, 200, map[string]any{
		"enabled": config.BrowserLoginConfig().Enabled,
		"pending": s.browserLoginMgr().list(),
	})
}

func (s *Server) browserLoginDone(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	taskID := strings.TrimSpace(r.PathValue("id"))
	if !s.browserLoginMgr().resolve(taskID, true) {
		writeErr(w, 404, "该任务没有待处理的登录接管")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) browserLoginCancel(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	taskID := strings.TrimSpace(r.PathValue("id"))
	if !s.browserLoginMgr().resolve(taskID, false) {
		writeErr(w, 404, "该任务没有待处理的登录接管")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

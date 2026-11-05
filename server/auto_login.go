package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/config"
	actool "github.com/Autumn-27/norma/tool"
)

// 自动化凭据登录(方案 增强项:与 B 的人工接管互补)。
//
// 用于无人值守/CI:按配置的 auth_profiles 自动完成表单登录(支持 $username/$password/$totp),
// 保存已认证会话态供 worker 复用。凭据可用 ${ENV} 注入,避免落盘。未配置任何 profile 时
// 不提供 auto_login 工具,零影响。登录执行走无头 playwright 助手,不依赖 B 的 VNC 栈。

// totpNow computes the current 6-digit TOTP (RFC 6238, SHA1, 30s step) for a base32
// secret. Returns "" when the secret is empty/invalid.
func totpNow(base32Secret string, t time.Time) string {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(base32Secret), " ", ""))
	if s == "" {
		return ""
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return ""
	}
	counter := uint64(t.Unix()) / 30
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	h := hmac.New(sha1.New, key)
	h.Write(buf[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[off]&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])) % 1000000
	return fmt.Sprintf("%06d", code)
}

// matchAuthProfile picks the first profile whose Match is a substring of target
// (case-insensitive). Returns nil when none match.
func matchAuthProfile(profiles []config.AuthProfile, target string) *config.AuthProfile {
	tl := strings.ToLower(target)
	for i := range profiles {
		m := strings.ToLower(strings.TrimSpace(profiles[i].Match))
		if m != "" && strings.Contains(tl, m) {
			return &profiles[i]
		}
	}
	return nil
}

// resolveAuthSteps substitutes $username/$password/$totp into each step value.
func resolveAuthSteps(p *config.AuthProfile, now time.Time) []config.AuthStep {
	totp := totpNow(p.TOTPSecret, now)
	rep := strings.NewReplacer("$username", p.Username, "$password", p.Password, "$totp", totp)
	out := make([]config.AuthStep, len(p.Steps))
	for i, s := range p.Steps {
		s.Value = rep.Replace(s.Value)
		out[i] = s
	}
	return out
}

func (s *Server) autoLoginTool() actool.CoreTool {
	return wrTool("auto_login",
		"自动化凭据登录:按配置的登录档案(auth_profiles)自动完成目标的表单登录(含 $username/$password/$totp),"+
			"保存已认证会话态并返回其路径,供后续 playwright-cli state-load 复用。"+
			"用于已配置凭据的无人值守场景;需要人工交互(如 SSO 推送/验证码)时改用 browser_login(authenticated-session)。",
		objSchema(map[string]any{
			"target": strParam("目标 host 或 URL,用于匹配登录档案"),
		}, "target"),
		s.runAutoLogin,
	)
}

func (s *Server) runAutoLogin(ctx context.Context, in json.RawMessage) (actool.Result, error) {
	profiles := config.AuthProfilesConfig()
	if len(profiles) == 0 {
		return actool.Errorf("未配置任何登录档案(auth_profiles);如需人工登录请用 browser_login"), nil
	}
	var args struct {
		Target string `json:"target"`
	}
	_ = json.Unmarshal(in, &args)
	args.Target = strings.TrimSpace(args.Target)
	if args.Target == "" {
		return actool.Errorf("target 为必填"), nil
	}
	p := matchAuthProfile(profiles, args.Target)
	if p == nil {
		return actool.Errorf("没有匹配该目标的登录档案(auth_profiles.match)"), nil
	}
	ri := agent.RunInfoFrom(ctx)
	if ri.TaskID == 0 {
		return actool.Errorf("auto_login 仅在任务上下文中可用"), nil
	}
	taskID := strconv.FormatInt(ri.TaskID, 10)
	taskDir := filepath.Join(s.m.dir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return actool.Errorf("创建任务目录失败: " + err.Error()), nil
	}
	statePath := filepath.Join(taskDir, "auth-state.json")

	payload := map[string]any{
		"login_url":            p.LoginURL,
		"steps":                resolveAuthSteps(p, time.Now()),
		"state_path":           statePath,
		"success_url_contains": p.SuccessURLContains,
		"success_selector":     p.SuccessSelector,
	}
	pj, _ := json.Marshal(payload)

	helper := filepath.Join(s.skillDir, "authenticated-session", "scripts", "auto-login.js")
	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c := exec.CommandContext(runCtx, "node", helper)
	c.Stdin = strings.NewReader(string(pj))
	out, err := c.CombinedOutput()
	if err != nil {
		return actool.Errorf("自动登录失败: " + err.Error() + "\n" + string(out)), nil
	}
	rel := filepath.Join("tasks", taskID, "auth-state.json")
	return actool.Text(fmt.Sprintf("自动登录完成。已保存会话态到: %s\n后续:\n  playwright-cli state-load %s\n  playwright-cli open <登录后页面>\n助手输出: %s",
		statePath, rel, strings.TrimSpace(string(out)))), nil
}

// seedAutoLoginTool seeds the tool row (worker/auto) when profiles are configured.
func (s *Server) seedAutoLoginTool() {
	if len(config.AuthProfilesConfig()) == 0 || s.m == nil || s.m.pg == nil {
		return
	}
	t := s.autoLoginTool()
	schema, _ := json.Marshal(t.InputSchema())
	agents, _ := json.Marshal([]string{"worker", "auto"})
	_ = s.m.pg.SeedTool("auto_login", t.Description(), schema, agents)
}

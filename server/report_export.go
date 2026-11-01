package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/report"
)

// 报告增强(方案 增强项):LLM 执行摘要 + HTML/PDF 报告。
//
// - GET /api/report/executive?task=<id>  → 纯文本执行摘要(LLM 合成;无 LLM 时返回空)
// - GET /api/report/html?task=<id>       → 自包含、可打印的 HTML 报告(默认含执行摘要)
// - GET /api/report/pdf?task=<id>        → 用无头 Chromium 把上面的 HTML 打成 PDF

const execSummarySystem = "你是安全评估报告的撰写者。基于给定的【已确认发现清单】写一段【执行摘要】:" +
	"面向管理者,说明整体风险态势、最需要关注的问题及其业务影响、以及总体修复方向。" +
	"要求:客观克制、不夸大、不堆砌;只依据给定发现,不臆造;3-6 句,中文纯文本,不用 Markdown 标题。" +
	"若清单为空,只回一句“本次未产生需报告的确认发现。”。"

// buildExecSummary synthesizes an executive summary from a task's findings via the
// global LLM provider. Returns "" (no error) when no LLM is configured.
func (s *Server) buildExecSummary(ctx context.Context, t *Task) (string, error) {
	if s.llmProv == nil {
		return "", nil
	}
	findings, _ := t.Store.ListByKind(db.KindFinding, 1000)
	digest := report.Digest(findings)
	user := "目标:" + t.Description + "\n\n已确认发现清单:\n" + digest
	if digest == "" {
		user = "目标:" + t.Description + "\n\n已确认发现清单:(空)"
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return streamCollectText(cctx, s.llmProv, execSummarySystem, user)
}

func (s *Server) reportInput(t *Task) report.Input {
	findings, _ := t.Store.ListByKind(db.KindFinding, 1000)
	counts := map[string]int{}
	for _, ty := range []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"} {
		if ns, _ := s.m.Assets().QueryByType(ty, 100000, 0); len(ns) > 0 {
			counts[ty] = len(ns)
		}
	}
	return report.Input{
		Title: t.Description, Goal: t.Goal, GeneratedAt: time.Now(),
		AssetCounts: counts, Findings: findings,
	}
}

func (s *Server) getReportExecutive(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	summary, err := s.buildExecSummary(r.Context(), t)
	if err != nil {
		writeErr(w, 502, "生成执行摘要失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"executive_summary": summary, "llm": s.llmProv != nil})
}

// reportHTMLFor builds the HTML report, including an LLM executive summary unless
// ?exec=0 is passed.
func (s *Server) reportHTMLFor(r *http.Request, t *Task) string {
	var exec string
	if r.URL.Query().Get("exec") != "0" {
		exec, _ = s.buildExecSummary(r.Context(), t)
	}
	return report.HTML(s.reportInput(t), exec)
}

func (s *Server) getReportHTML(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(s.reportHTMLFor(r, t)))
}

func (s *Server) getReportPDF(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	htmlStr := s.reportHTMLFor(r, t)
	pdf, err := htmlToPDF(r.Context(), htmlStr)
	if err != nil {
		writeErr(w, 501, "生成 PDF 失败("+err.Error()+")。可改用 /api/report/html 获取可打印 HTML(浏览器另存为 PDF)。")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\"report.pdf\"")
	w.WriteHeader(200)
	_, _ = w.Write(pdf)
}

// htmlToPDF renders HTML to PDF using headless Chromium's --print-to-pdf. Returns
// an error (with guidance) when no chromium binary is available.
func htmlToPDF(ctx context.Context, htmlStr string) ([]byte, error) {
	bin := ""
	for _, c := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(c); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		return nil, fmt.Errorf("未找到 chromium")
	}
	dir, err := os.MkdirTemp("", "artex-pdf-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	inPath := filepath.Join(dir, "report.html")
	outPath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(inPath, []byte(htmlStr), 0o600); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin,
		"--headless", "--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu",
		"--no-pdf-header-footer", "--print-to-pdf="+outPath, "file://"+inPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("chromium: %v (%s)", err, string(out))
	}
	return os.ReadFile(outPath)
}

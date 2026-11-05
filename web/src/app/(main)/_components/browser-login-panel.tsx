"use client";

import * as React from "react";

import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";

// SSO 登录接管面板(方案 工作流 B)。
// 轮询待处理的登录接管;出现时弹出 noVNC 面板,人工在其中完成 SSO/MFA 登录后点"我已登录完成"。
// 后端未开启(enabled=false)时轮询到 enabled:false,面板不显示。

type Pending = { task_id: string; login_url: string; novnc_url: string; created_at: string };

export function BrowserLoginPanel() {
  const [pending, setPending] = React.useState<Pending | null>(null);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    const poll = async () => {
      try {
        const r = await api.browserLoginPending();
        if (!alive) return;
        setPending(r.enabled && r.pending.length > 0 ? r.pending[0] : null);
      } catch {
        // ignore transient errors
      }
    };
    void poll();
    const t = setInterval(() => {
      void poll();
    }, 4000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  const done = async () => {
    if (!pending) return;
    setBusy(true);
    try {
      await api.browserLoginDone(pending.task_id);
      toast.success("已提交登录完成，正在保存会话态…");
      setPending(null);
    } catch (e) {
      toast.error(`提交失败：${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  const cancel = async () => {
    if (!pending) return;
    setBusy(true);
    try {
      await api.browserLoginCancel(pending.task_id);
      setPending(null);
    } catch (e) {
      toast.error(`取消失败：${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  if (!pending) return null;

  return (
    <Dialog open={true} onOpenChange={() => undefined}>
      <DialogContent className="flex h-[85vh] max-w-5xl flex-col">
        <DialogHeader>
          <DialogTitle>SSO 登录接管 · 任务 #{pending.task_id}</DialogTitle>
          <DialogDescription>
            请在下面的浏览器里完成登录（含 SSO / MFA），目标：{pending.login_url}。登录成功后点「我已登录完成」，
            系统会保存已认证会话并让测试继续。
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-hidden rounded-md border">
          <iframe title="server-browser" src={pending.novnc_url} className="h-full w-full" />
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={cancel} disabled={busy}>
            取消接管
          </Button>
          <Button onClick={done} disabled={busy}>
            {busy ? "处理中…" : "我已登录完成"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

"use client";

import * as React from "react";

import { DownloadIcon, PlayIcon, RefreshCwIcon, SearchIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { InventoryDomain, ScenarioConstraint, TaskTemplate } from "@/lib/types";

// 域名清单(InfoSec Portal)+ 场景化发起测试(方案 工作流 C 第二阶段)。
// 未配置真实 Portal 清单接口时,后端回落到内置 mock 数据,功能即刻可用。

export default function InventoryPage() {
  const [source, setSource] = React.useState("");
  const [domains, setDomains] = React.useState<InventoryDomain[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [search, setSearch] = React.useState("");
  const [syncing, setSyncing] = React.useState(false);
  const [launchFor, setLaunchFor] = React.useState<InventoryDomain | null>(null);

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .inventoryDomains()
      .then((r) => {
        setSource(r.source);
        setDomains(r.domains);
      })
      .catch((e) => toast.error(`拉取域名清单失败：${(e as Error).message}`))
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(() => {
    load();
  }, [load]);

  const syncAll = async () => {
    setSyncing(true);
    try {
      const r = await api.inventorySync({ create_company: true });
      toast.success(`已同步到资产库：域名 ${r.synced?.domain ?? 0}、IP ${r.synced?.ip ?? 0}`);
      for (const wmsg of r.warnings ?? []) toast.warning(wmsg);
    } catch (e) {
      toast.error(`同步失败：${(e as Error).message}`);
    } finally {
      setSyncing(false);
    }
  };

  const filtered = domains.filter((d) => {
    if (!search.trim()) return true;
    const q = search.trim().toLowerCase();
    return (
      d.domain.toLowerCase().includes(q) ||
      (d.company ?? "").toLowerCase().includes(q) ||
      (d.tags ?? []).some((t) => t.toLowerCase().includes(q))
    );
  });

  return (
    <div className="p-4 md:p-6">
      <div className="mb-4 flex items-start justify-between gap-4">
        <div>
          <h1 className="font-semibold text-xl">域名清单</h1>
          <p className="text-muted-foreground text-sm">
            来自 InfoSec Portal 的域名资产{source ? `（数据源：${source}）` : ""}
            。点「发起测试」选择场景即可建任务，免去手写任务描述。
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCwIcon className="mr-1 size-4" /> 刷新
          </Button>
          <Button size="sm" onClick={syncAll} disabled={syncing || domains.length === 0}>
            <DownloadIcon className="mr-1 size-4" /> {syncing ? "同步中…" : "同步到资产库"}
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between text-base">
            <span>域名（{filtered.length}）</span>
            <div className="relative w-64">
              <SearchIcon className="-translate-y-1/2 absolute top-1/2 left-2 size-4 text-muted-foreground" />
              <Input
                className="pl-8"
                placeholder="搜索域名 / 公司 / 标签"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {loading && <p className="py-8 text-center text-muted-foreground text-sm">加载中…</p>}
          {!loading && filtered.length === 0 && (
            <p className="py-8 text-center text-muted-foreground text-sm">暂无域名。</p>
          )}
          {!loading && filtered.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>域名</TableHead>
                  <TableHead>公司</TableHead>
                  <TableHead>IP</TableHead>
                  <TableHead>标签</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((d) => (
                  <TableRow key={d.domain}>
                    <TableCell className="font-medium">{d.domain}</TableCell>
                    <TableCell className="text-muted-foreground">{d.company ?? "—"}</TableCell>
                    <TableCell className="text-muted-foreground">{(d.ips ?? []).join(", ") || "—"}</TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {(d.tags ?? []).map((t) => (
                          <Badge key={t} variant="secondary">
                            {t}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button size="sm" variant="outline" onClick={() => setLaunchFor(d)}>
                        <PlayIcon className="mr-1 size-4" /> 发起测试
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <LaunchDialog domain={launchFor} onClose={() => setLaunchFor(null)} />
    </div>
  );
}

function LaunchDialog({ domain, onClose }: { domain: InventoryDomain | null; onClose: () => void }) {
  const [scenarios, setScenarios] = React.useState<TaskTemplate[]>([]);
  const [constraints, setConstraints] = React.useState<ScenarioConstraint[]>([]);
  const [constraint, setConstraint] = React.useState<string>("");
  const [profile, setProfile] = React.useState(true);
  const [selected, setSelected] = React.useState<Set<number>>(new Set());
  const [extra, setExtra] = React.useState("");
  const [name, setName] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);

  const open = domain !== null;

  React.useEffect(() => {
    if (!open) return;
    setSelected(new Set());
    setExtra("");
    setName("");
    setProfile(true);
    api
      .scenarios()
      .then((r) => {
        setScenarios(r.scenarios);
        setConstraints(r.constraints);
        setConstraint(r.defaultConstraint);
      })
      .catch((e) => toast.error(`加载场景失败：${(e as Error).message}`));
  }, [open]);

  const toggle = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const submit = async () => {
    if (!domain) return;
    if (selected.size === 0 && !extra.trim()) {
      toast.error("至少选择一个场景，或填写补充说明");
      return;
    }
    setSubmitting(true);
    try {
      const task = await api.launchScenario({
        target: domain.domain,
        scenario_ids: [...selected],
        constraint,
        profile,
        extra: extra.trim(),
        name: name.trim(),
      });
      toast.success(`已创建任务 #${task.id}`);
      onClose();
    } catch (e) {
      toast.error(`发起失败：${(e as Error).message}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>发起测试</DialogTitle>
          <DialogDescription>目标：{domain?.domain}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-2">
            <Label>选择场景（可多选）</Label>
            <div className="max-h-64 space-y-2 overflow-y-auto rounded-md border p-3">
              {scenarios.length === 0 ? (
                <p className="text-muted-foreground text-sm">暂无场景，可在「任务模板」中新增。</p>
              ) : (
                scenarios.map((sc) => (
                  <button
                    key={sc.id}
                    type="button"
                    className="flex w-full cursor-pointer items-start gap-2 text-left"
                    onClick={() => toggle(sc.id)}
                  >
                    <Checkbox checked={selected.has(sc.id)} className="pointer-events-none mt-0.5" tabIndex={-1} />
                    <span className="leading-tight">
                      <span className="font-medium text-sm">{sc.name}</span>
                      {sc.description ? (
                        <span className="block text-muted-foreground text-xs">{sc.description}</span>
                      ) : null}
                    </span>
                  </button>
                ))
              )}
            </div>
          </div>

          <div className="space-y-2">
            <Label>约束等级（ROE）</Label>
            <Select value={constraint} onValueChange={setConstraint}>
              <SelectTrigger>
                <SelectValue placeholder="选择约束等级" />
              </SelectTrigger>
              <SelectContent>
                {constraints.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {constraints.find((c) => c.id === constraint)?.desc ? (
              <p className="text-muted-foreground text-xs">{constraints.find((c) => c.id === constraint)?.desc}</p>
            ) : null}
          </div>

          <button
            type="button"
            className="flex w-full cursor-pointer items-start gap-2 text-left"
            onClick={() => setProfile((v) => !v)}
          >
            <Checkbox checked={profile} className="pointer-events-none mt-0.5" tabIndex={-1} />
            <span className="leading-tight">
              <span className="font-medium text-sm">测试时顺带采集域名画像</span>
              <span className="block text-muted-foreground text-xs">
                记录这个域名是做什么的、是否 SSO、能看到什么数据，并对关键页面截图
              </span>
            </span>
          </button>

          <div className="space-y-2">
            <Label htmlFor="scenario-extra">补充说明（可选）</Label>
            <Textarea
              id="scenario-extra"
              placeholder="额外的测试重点 / 约束 / 账号提示等"
              value={extra}
              onChange={(e) => setExtra(e.target.value)}
              rows={3}
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="scenario-name">任务名称（可选）</Label>
            <Input
              id="scenario-name"
              placeholder={`场景测试：${domain?.domain ?? ""}`}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? "创建中…" : "创建任务"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

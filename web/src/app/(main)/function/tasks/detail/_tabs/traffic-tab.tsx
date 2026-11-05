"use client";

import * as React from "react";

import { toast } from "sonner";

import { HttpCodeBlock } from "@/components/http-code-block";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { TrafficDetail, TrafficExchange } from "@/lib/types";
import { cn } from "@/lib/utils";

// TrafficTab shows the HTTP exchanges recorded for THIS task — the global traffic
// store filtered to the task's declared scope hosts (GET /api/tasks/{id}/traffic).
// Burp-style Proxy history: a dense request list on top, request/response side by
// side below; selecting a row loads its full exchange (GET /api/traffic/exchange).

function statusTone(code: number): string {
  if (code >= 500) return "text-red-600 dark:text-red-400";
  if (code >= 400) return "text-amber-600 dark:text-amber-400";
  if (code >= 300) return "text-blue-600 dark:text-blue-400";
  if (code >= 200) return "text-emerald-600 dark:text-emerald-400";
  return "text-muted-foreground";
}

function fmtTime(ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString();
}

function fmtLen(n: number): string {
  if (!n || n < 0) return "0";
  if (n < 1024) return `${n}`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)}K`;
  return `${(n / 1024 / 1024).toFixed(1)}M`;
}

function pathOf(url: string): string {
  try {
    const u = new URL(url);
    return (u.pathname || "/") + u.search;
  } catch {
    return url;
  }
}

export function TrafficTab({ taskId }: { taskId: string }) {
  const [rows, setRows] = React.useState<TrafficExchange[]>([]);
  const [hosts, setHosts] = React.useState<string[]>([]);
  const [enabled, setEnabled] = React.useState(true);
  const [loading, setLoading] = React.useState(false);
  const [filter, setFilter] = React.useState("");
  const [selectedId, setSelectedId] = React.useState<string | null>(null);
  const [detail, setDetail] = React.useState<TrafficDetail | null>(null);
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [side, setSide] = React.useState<"both" | "req" | "resp">("both");

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const r = await api.taskTraffic(taskId);
      setRows(r.exchanges ?? []);
      setHosts(r.hosts ?? []);
      setEnabled(r.enabled !== false);
    } catch (e) {
      toast.error(`加载流量失败：${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, [taskId]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const select = React.useCallback(async (ex: TrafficExchange) => {
    setSelectedId(ex.id);
    setDetail(null);
    setDetailLoading(true);
    try {
      setDetail(await api.trafficExchange(ex.id));
    } catch (e) {
      toast.error(`加载详情失败：${(e as Error).message}`);
    } finally {
      setDetailLoading(false);
    }
  }, []);

  const filtered = React.useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter(
      (r) =>
        r.url.toLowerCase().includes(q) ||
        r.method.toLowerCase().includes(q) ||
        String(r.status).includes(q),
    );
  }, [rows, filter]);

  const selected = filtered.find((r) => r.id === selectedId) ?? null;

  return (
    <div className="flex flex-col gap-3">
      {/* toolbar */}
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="text-muted-foreground">本任务范围内已录制的 HTTP 流量</span>
        {hosts.map((h) => (
          <Badge key={h} variant="outline" className="font-mono text-xs">
            {h}
          </Badge>
        ))}
        <input
          className="ml-auto h-8 w-56 rounded-md border bg-background px-2 text-sm outline-none focus:ring-1 focus:ring-ring"
          placeholder="过滤 URL / 方法 / 状态码"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
          刷新
        </Button>
      </div>

      {!enabled && (
        <div className="rounded-md border border-amber-400/50 bg-amber-400/5 p-3 text-sm">
          流量录制未开启(系统配置里打开「流量捕获」后,本任务对范围内主机的请求会在此逐条呈现完整请求/响应)。
        </div>
      )}

      {/* request list (Burp-style history table) */}
      <div className="overflow-auto rounded-lg border" style={{ maxHeight: "38vh" }}>
        <table className="w-full border-collapse text-xs">
          <thead className="sticky top-0 z-10 bg-muted/80 text-muted-foreground backdrop-blur">
            <tr>
              <th className="px-2 py-1.5 text-left font-medium">#</th>
              <th className="px-2 py-1.5 text-left font-medium">方法</th>
              <th className="px-2 py-1.5 text-right font-medium">状态</th>
              <th className="px-2 py-1.5 text-left font-medium">路径</th>
              <th className="px-2 py-1.5 text-left font-medium">主机</th>
              <th className="px-2 py-1.5 text-right font-medium">长度</th>
              <th className="px-2 py-1.5 text-left font-medium">时间</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((ex, i) => (
              // biome-ignore lint/a11y/useKeyWithClickEvents: row selection; detail pane updates below
              <tr
                key={ex.id}
                onClick={() => void select(ex)}
                className={cn(
                  "cursor-pointer border-t hover:bg-muted/40",
                  ex.id === selectedId && "bg-primary/10 hover:bg-primary/10",
                )}
              >
                <td className="px-2 py-1 text-muted-foreground tabular-nums">{i + 1}</td>
                <td className="px-2 py-1 font-mono font-medium">{ex.method}</td>
                <td className={cn("px-2 py-1 text-right font-mono tabular-nums", statusTone(ex.status))}>
                  {ex.status || "—"}
                </td>
                <td className="max-w-[420px] truncate px-2 py-1 font-mono" title={ex.url}>
                  {pathOf(ex.url)}
                </td>
                <td className="truncate px-2 py-1 text-muted-foreground">{ex.host}</td>
                <td className="px-2 py-1 text-right text-muted-foreground tabular-nums">{fmtLen(ex.resp_len)}</td>
                <td className="whitespace-nowrap px-2 py-1 text-muted-foreground">{fmtTime(ex.ts)}</td>
              </tr>
            ))}
            {filtered.length === 0 && (
              <tr>
                <td colSpan={7} className="px-3 py-10 text-center text-muted-foreground">
                  {loading ? "加载中…" : rows.length === 0 ? "暂无本任务范围内的流量记录" : "无匹配结果"}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* detail: request / response side by side (Burp message editors) */}
      <div className="flex items-center gap-2">
        <span className="text-muted-foreground text-xs">
          {selected ? (
            <span className="font-mono">
              {selected.method} {selected.url}
            </span>
          ) : (
            "选择上方一条请求查看完整请求/响应"
          )}
        </span>
        {selected && (
          <div className="ml-auto flex gap-1">
            {(["both", "req", "resp"] as const).map((s) => (
              <Button key={s} size="sm" variant={side === s ? "default" : "outline"} onClick={() => setSide(s)}>
                {s === "both" ? "并排" : s === "req" ? "请求" : "响应"}
              </Button>
            ))}
          </div>
        )}
      </div>

      {selected && (
        <div
          className={cn(
            "grid gap-3",
            side === "both" ? "grid-cols-1 lg:grid-cols-2" : "grid-cols-1",
          )}
        >
          {side !== "resp" && (
            <div className="rounded-lg border">
              <div className="border-b bg-muted/50 px-3 py-1.5 font-medium text-xs">请求</div>
              <div className="max-h-[42vh] overflow-auto p-2">
                {detailLoading ? (
                  <div className="py-8 text-center text-muted-foreground text-sm">加载中…</div>
                ) : (
                  <HttpCodeBlock raw={detail?.req ?? ""} />
                )}
              </div>
            </div>
          )}
          {side !== "req" && (
            <div className="rounded-lg border">
              <div className="border-b bg-muted/50 px-3 py-1.5 font-medium text-xs">响应</div>
              <div className="max-h-[42vh] overflow-auto p-2">
                {detailLoading ? (
                  <div className="py-8 text-center text-muted-foreground text-sm">加载中…</div>
                ) : (
                  <HttpCodeBlock raw={detail?.resp ?? ""} />
                )}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

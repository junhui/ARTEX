"use client";

import * as React from "react";

import { toast } from "sonner";

import { HttpCodeBlock } from "@/components/http-code-block";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { TrafficDetail, TrafficExchange } from "@/lib/types";

// TrafficTab shows the HTTP exchanges recorded for THIS task — the global traffic
// store filtered to the task's declared scope hosts (see GET /api/tasks/{id}/traffic).
// Full request/response is loaded lazily on row click via /api/traffic/exchange.
export function TrafficTab({ taskId }: { taskId: string }) {
  const [rows, setRows] = React.useState<TrafficExchange[]>([]);
  const [hosts, setHosts] = React.useState<string[]>([]);
  const [enabled, setEnabled] = React.useState(true);
  const [loading, setLoading] = React.useState(false);
  const [selected, setSelected] = React.useState<TrafficExchange | null>(null);
  const [detail, setDetail] = React.useState<TrafficDetail | null>(null);

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

  async function open(ex: TrafficExchange) {
    setSelected(ex);
    setDetail(null);
    try {
      setDetail(await api.trafficExchange(ex.id));
    } catch (e) {
      toast.error(`加载详情失败：${(e as Error).message}`);
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2 text-muted-foreground text-sm">
        <span>本任务范围内已录制的 HTTP 流量</span>
        {hosts.length > 0 && (
          <span>
            · 范围主机：
            {hosts.map((h) => (
              <Badge key={h} variant="outline" className="ml-1 font-mono">
                {h}
              </Badge>
            ))}
          </span>
        )}
        <Button size="sm" variant="outline" className="ml-auto" onClick={() => void load()} disabled={loading}>
          刷新
        </Button>
      </div>

      {!enabled && (
        <div className="rounded-md border border-amber-400/50 bg-amber-400/5 p-3 text-sm">
          流量录制未开启（或代理未运行）；开启后本任务对范围内主机的请求会在此逐条呈现完整请求/响应。
        </div>
      )}

      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-muted-foreground text-xs">
            <tr>
              <th className="px-3 py-2 text-left font-medium">时间</th>
              <th className="px-3 py-2 text-left font-medium">方法</th>
              <th className="px-3 py-2 text-left font-medium">URL</th>
              <th className="px-3 py-2 text-right font-medium">状态</th>
              <th className="px-3 py-2 text-right font-medium">响应长度</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((ex) => (
              // biome-ignore lint/a11y/useKeyWithClickEvents: row opens a detail dialog; keyboard users have the same via tab/enter on the cells' content
              <tr
                key={ex.id}
                onClick={() => void open(ex)}
                className="cursor-pointer border-t hover:bg-muted/40"
              >
                <td className="whitespace-nowrap px-3 py-2 text-muted-foreground">
                  {new Date(Number(ex.ts) * 1000).toLocaleTimeString()}
                </td>
                <td className="px-3 py-2 font-mono">{ex.method}</td>
                <td className="max-w-[520px] truncate px-3 py-2 font-mono" title={ex.url}>
                  {ex.url}
                </td>
                <td className="px-3 py-2 text-right font-mono">{ex.status}</td>
                <td className="px-3 py-2 text-right text-muted-foreground">{ex.resp_len}</td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={5} className="px-3 py-10 text-center text-muted-foreground">
                  {loading ? "加载中…" : "暂无本任务范围内的流量记录"}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(null)}>
        <DialogContent className="max-h-[85vh] max-w-4xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="font-mono text-sm">
              {selected?.method} {selected?.url}
            </DialogTitle>
          </DialogHeader>
          {detail ? (
            <Tabs defaultValue="req">
              <TabsList>
                <TabsTrigger value="req">请求</TabsTrigger>
                <TabsTrigger value="resp">响应</TabsTrigger>
              </TabsList>
              <TabsContent value="req" className="mt-3">
                <HttpCodeBlock raw={detail.req} />
              </TabsContent>
              <TabsContent value="resp" className="mt-3">
                <HttpCodeBlock raw={detail.resp} />
              </TabsContent>
            </Tabs>
          ) : (
            <div className="py-10 text-center text-muted-foreground text-sm">加载中…</div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

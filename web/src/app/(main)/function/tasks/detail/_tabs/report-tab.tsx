"use client";

import * as React from "react";

import { CheckIcon, CopyIcon, FileTextIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { copyText } from "@/lib/utils";

export function ReportTab({ taskId }: { taskId: string }) {
  const [report, setReport] = React.useState<string>("");
  const [htmlUrl, setHtmlUrl] = React.useState<string>("");
  const [loading, setLoading] = React.useState(true);
  const [copied, setCopied] = React.useState(false);
  const [exporting, setExporting] = React.useState(false);

  async function openHtml() {
    setExporting(true);
    try {
      const blob = await api.reportBlob("html", taskId);
      const url = URL.createObjectURL(blob);
      window.open(url, "_blank", "noopener");
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    } catch (e) {
      toast.error(`导出 HTML 失败：${(e as Error).message}`);
    } finally {
      setExporting(false);
    }
  }

  async function downloadPdf() {
    setExporting(true);
    try {
      const blob = await api.reportBlob("pdf", taskId);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `report-${taskId}.pdf`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    } catch (e) {
      toast.error(`导出 PDF 失败：${(e as Error).message}`);
    } finally {
      setExporting(false);
    }
  }

  React.useEffect(() => {
    let active = true;
    let objUrl = "";
    setLoading(true);
    // Markdown (for copy) + styled HTML (for the inline preview) in parallel.
    void Promise.allSettled([api.report(taskId), api.reportBlob("html", taskId)])
      .then(([md, html]) => {
        if (!active) return;
        setReport(md.status === "fulfilled" ? md.value : "");
        if (html.status === "fulfilled") {
          objUrl = URL.createObjectURL(html.value);
          setHtmlUrl(objUrl);
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      if (objUrl) URL.revokeObjectURL(objUrl);
    };
  }, [taskId]);

  async function copy() {
    if (!report) return;
    const ok = await copyText(report);
    if (ok) {
      setCopied(true);
      toast.success("已复制 Markdown");
      setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error("复制失败，请手动选择文本复制");
    }
  }

  let content: React.ReactNode;
  if (loading) {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed py-20 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        加载中…
      </div>
    );
  } else if (htmlUrl) {
    content = (
      <div className="overflow-hidden rounded-lg border bg-white shadow-sm">
        <iframe title="report" src={htmlUrl} className="h-[72vh] w-full border-0" />
      </div>
    );
  } else {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed py-20 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        暂无报告
      </div>
    );
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-sm">
          <FileTextIcon className="size-4" /> 渗透测试报告
        </CardTitle>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={copy} disabled={!report}>
            {copied ? <CheckIcon /> : <CopyIcon />} 复制 MD
          </Button>
          <Button size="sm" variant="outline" onClick={openHtml} disabled={exporting || !htmlUrl}>
            <FileTextIcon /> HTML
          </Button>
          <Button size="sm" variant="outline" onClick={downloadPdf} disabled={exporting || !htmlUrl}>
            <FileTextIcon /> PDF
          </Button>
        </div>
      </CardHeader>
      <CardContent>{content}</CardContent>
    </Card>
  );
}

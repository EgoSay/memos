import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getRequestToken } from "@/connect";

type Report = { memos: number; attachments: number; originals: number; warnings: string[]; complete: boolean };
type Preview = {
  token: string;
  digest: string;
  createdTs: number;
  memos: number;
  attachments: number;
  originals: number;
  warnings: string[];
};
async function request(path: string, options: RequestInit = {}) {
  const token = await getRequestToken();
  const headers = new Headers(options.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  return fetch(`/api/v1/journal/backups${path}`, { ...options, headers, credentials: "same-origin" });
}
function Warnings({ warnings }: { warnings: string[] }) {
  if (!warnings.length) return null;
  return (
    <details>
      <summary className="min-h-11 cursor-pointer py-3 text-sm">恢复说明与媒体缺口（{warnings.length}）</summary>
      <ul className="list-disc space-y-2 pl-5 text-xs leading-5 text-muted-foreground">
        {warnings.map((warning, index) => (
          <li key={`${index}-${warning}`}>{warning}</li>
        ))}
      </ul>
    </details>
  );
}
export default function JournalBackup() {
  const [history, setHistory] = useState(false);
  const [file, setFile] = useState<File>();
  const [preview, setPreview] = useState<Preview>();
  const [report, setReport] = useState<Report>();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const run = async (kind: string, work: () => Promise<void>) => {
    setBusy(kind);
    setError("");
    setMessage("");
    try {
      await work();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "操作未完成，请重试。");
    } finally {
      setBusy("");
    }
  };
  const download = () =>
    run("export", async () => {
      const response = await request(`/export?history=${history}`);
      if (!response.ok) {
        const result = await response.json();
        throw new Error(result.message);
      }
      const blob = await response.blob();
      const href = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = href;
      link.download = `personal-journal-${new Date().toISOString().slice(0, 10)}.zip`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(href), 60_000);
      setMessage("档案已准备下载。请检查浏览器下载结果，并保存到你选择的独立备份位置。");
    });
  const inspect = () =>
    run("preview", async () => {
      if (!file) return;
      setPreview(undefined);
      setReport(undefined);
      const response = await request("/preview", { method: "POST", body: file, headers: { "Content-Type": "application/zip" } });
      const value = await response.json();
      if (!response.ok) throw new Error(value.message);
      setPreview(value);
    });
  const restore = () =>
    run("restore", async () => {
      if (!preview) return;
      const response = await request(`/${preview.token}/restore`, {
        method: "POST",
        body: JSON.stringify({ digest: preview.digest }),
        headers: { "Content-Type": "application/json" },
      });
      const value = await response.json();
      if (!response.ok) {
        if (value.report) setReport(value.report);
        throw new Error(value.message ?? "恢复未完成；重试同一档案不会重复导入。");
      }
      setReport(value);
    });
  return (
    <div className="mx-auto w-full max-w-2xl space-y-8 pb-12 pt-5 sm:pt-10">
      <header>
        <h1 className="font-serif text-3xl font-medium tracking-tight">数据与备份</h1>
        <p className="mt-3 text-sm leading-6 text-muted-foreground">把自己的日子带走，也能在另一处接着保存。</p>
      </header>
      <section className="space-y-4 rounded-xl border border-border/70 bg-card p-5">
        <h2 className="text-lg font-medium">下载可迁移档案</h2>
        <p className="text-sm leading-6 text-muted-foreground">
          包含本人记录、日期、标签、关系、管理中的媒体与可用原件，以及主动保存的 AI 洞察。解压后可以直接阅读 Markdown 和媒体文件。
        </p>
        <label className="flex min-h-11 items-center gap-3 text-sm">
          <input type="checkbox" checked={history} disabled={Boolean(busy)} onChange={(event) => setHistory(event.target.checked)} />
          同时包含最近 30 天的编辑历史
        </label>
        <p className="text-xs leading-5 text-muted-foreground">
          不包含草稿或最近删除。分享与同步只保留脱敏规则和历史映射；访问令牌、链接密钥、Webhook
          地址与签名密钥不会导出。原件与外链缺口会列入报告。
        </p>
        <Button className="min-h-11" disabled={Boolean(busy)} onClick={() => void download()}>
          {busy === "export" ? "正在整理档案…" : "下载 ZIP 档案"}
        </Button>
      </section>
      <section className="space-y-4 rounded-xl border border-border/70 bg-card p-5">
        <h2 className="text-lg font-medium">从档案恢复</h2>
        <p className="text-sm leading-6 text-muted-foreground">
          先检查文件和媒体校验和，再决定恢复。不会覆盖已有记录；同一档案重复恢复不会产生重复副本。
        </p>
        <Input
          aria-label="选择要检查的 ZIP 备份"
          className="min-h-11 py-2"
          type="file"
          accept=".zip,application/zip"
          disabled={Boolean(busy)}
          onChange={(event) => {
            setFile(event.target.files?.[0]);
            setPreview(undefined);
            setReport(undefined);
            setError("");
          }}
        />
        <p className="text-xs text-muted-foreground">单份档案最大 1 GiB；检查阶段不会导入内容。</p>
        <Button variant="outline" className="min-h-11" disabled={!file || Boolean(busy)} onClick={() => void inspect()}>
          {busy === "preview" ? "正在核对文件…" : "检查备份"}
        </Button>
        {preview && !report?.complete && (
          <div className="space-y-4 border-t border-border pt-4">
            <p className="text-sm">档案日期：{new Date(preview.createdTs * 1000).toLocaleString()}</p>
            <p className="text-sm">
              {preview.memos} 条记录 · {preview.attachments} 个附件 · {preview.originals} 个原始文件
            </p>
            <p className="break-all text-xs text-muted-foreground">SHA-256：{preview.digest}</p>
            <Warnings warnings={preview.warnings} />
            <p className="text-sm leading-6">
              恢复会暂停本账户现有分享与同步。恢复内容保持私密，旧链接不会复活，历史任务不会重发；需要重新确认分享范围并填写同步地址。
            </p>
            <Button className="min-h-11 h-auto whitespace-normal py-3" disabled={Boolean(busy)} onClick={() => void restore()}>
              {busy === "restore" ? "正在恢复，原件较多时需要稍等…" : "暂停分享与同步，并恢复为私密记录"}
            </Button>
          </div>
        )}
        {report && (
          <div role="status" className="space-y-2 border-t border-border pt-4">
            <p className="font-medium">{report.complete ? "档案恢复完成" : "已保留部分恢复结果"}</p>
            <p className="text-sm">
              {report.memos} 条记录 · {report.attachments} 个附件 · {report.originals} 个原件
            </p>
            <Warnings warnings={report.warnings} />
          </div>
        )}
      </section>
      {message && (
        <p role="status" className="text-sm leading-6 text-muted-foreground">
          {message}
        </p>
      )}
      {error && (
        <p role="alert" className="text-sm leading-6 text-destructive">
          {error}
        </p>
      )}
      <p className="text-xs leading-6 text-muted-foreground">
        这是可迁移的个人资料档案；整实例数据库与媒体备份由部署运维另行保存。重要副本应放到独立设备或你选择的存储位置，同盘副本无法应对磁盘损坏。自托管不等于端到端加密，已下载副本也不会随应用内删除自动消失。
      </p>
    </div>
  );
}

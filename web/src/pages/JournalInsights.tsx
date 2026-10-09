import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import JournalSourcePicker from "@/components/JournalSourcePicker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import useCurrentUser from "@/hooks/useCurrentUser";
import { journalRequest } from "@/hooks/useJournalQueries";

interface Insight {
  id: string;
  text: string;
  citations: string[];
  sources: { name: string; fingerprint: string }[];
  createdTs: number;
  stale: boolean;
}
interface Config {
  providerId: string;
  model: string;
}
interface AISettings {
  providers: { id: string; title: string; endpoint: string }[];
  config: Config;
}

export default function JournalInsights() {
  const user = useCurrentUser();
  const client = useQueryClient();
  const [params] = useSearchParams();
  const [selected, setSelected] = useState<string[]>(() => (params.get("memo") ? [params.get("memo") as string] : []));
  const [result, setResult] = useState<Insight | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [showHistory, setShowHistory] = useState(false);
  const [config, setConfig] = useState<Config | null>(null);
  const controller = useRef<AbortController | null>(null);
  const configQuery = useQuery({ queryKey: ["journal", user?.name, "ai-config"], queryFn: () => journalRequest<AISettings>("/ai/config") });
  const history = useQuery({
    queryKey: ["journal", user?.name, "insights"],
    queryFn: () => journalRequest<Insight[]>("/insights"),
    enabled: showHistory,
  });
  const saveConfig = useMutation({
    mutationFn: (value: Config) => journalRequest<Config>("/ai/config", { method: "PUT", body: JSON.stringify(value) }),
    onSuccess: () => {
      setConfig(null);
      void configQuery.refetch();
    },
  });
  useEffect(() => () => controller.current?.abort(), []);
  const settings = config ?? configQuery.data?.config ?? { providerId: "", model: "" };
  const provider = configQuery.data?.providers.find((entry) => entry.id === configQuery.data?.config.providerId);
  const generate = async () => {
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setError("");
    setResult(null);
    setSaved(false);
    try {
      const next = await journalRequest<Insight>("/insights/generate", {
        method: "POST",
        body: JSON.stringify({ memoNames: selected }),
        signal: request.signal,
      });
      if (!request.signal.aborted) setResult(next);
    } catch (e) {
      if (!request.signal.aborted) setError(e instanceof Error ? e.message : "暂时无法生成洞察，请稍后重试。");
    } finally {
      if (controller.current === request) setBusy(false);
    }
  };
  const save = async () => {
    if (!result) return;
    try {
      await journalRequest("/insights", { method: "POST", body: JSON.stringify(result) });
      setSaved(true);
      void client.invalidateQueries({ queryKey: ["journal", user?.name, "insights"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : "没有保存成功。");
    }
  };
  return (
    <main className="mx-auto max-w-2xl space-y-7 pb-16 pt-4 sm:pt-8">
      <Link to="/journal" className="inline-flex min-h-11 items-center gap-2 text-sm text-muted-foreground">
        <ArrowLeftIcon className="size-4" />
        随便看看
      </Link>
      <header>
        <h1 className="font-serif text-3xl font-medium">AI 洞察</h1>
        <p className="mt-3 text-sm leading-7 text-muted-foreground">选几条记录，换个角度看看。原来的文字始终留在那里。</p>
      </header>
      <details className="rounded-lg border border-border/60 p-4" open={!configQuery.data?.config.providerId || undefined}>
        <summary className="cursor-pointer text-sm">{provider ? `当前服务：${provider.title}` : "配置 AI 服务"}</summary>
        <div className="mt-4 space-y-3 text-sm">
          {configQuery.isError && <p role="alert">服务设置读取失败，请刷新重试。</p>}
          {!configQuery.data?.providers.length && (
            <p>
              先在
              <Link className="underline" to="/setting#ai">
                设置中添加 OpenAI 兼容服务
              </Link>
              ，然后选择服务和模型。
            </p>
          )}
          <label className="block space-y-2">
            <span>服务</span>
            <select
              className="min-h-11 w-full rounded-md border bg-background px-3"
              value={settings.providerId}
              onChange={(e) => setConfig({ ...settings, providerId: e.target.value })}
            >
              <option value="">选择服务</option>
              {configQuery.data?.providers.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.title}
                </option>
              ))}
            </select>
          </label>
          <label className="block space-y-2">
            <span>模型名称</span>
            <Input
              value={settings.model}
              onChange={(e) => setConfig({ ...settings, model: e.target.value })}
              placeholder="服务提供的模型名称"
            />
          </label>
          <p className="leading-6 text-muted-foreground">
            点击“开始看看”后，所选正文和日期会发送到该服务。不会发送照片、原音或其他记录。取消不能撤回已经发送的内容。
          </p>
          <Button
            variant="outline"
            disabled={!settings.providerId || !settings.model || saveConfig.isPending}
            onClick={() => saveConfig.mutate(settings)}
          >
            保存设置
          </Button>
          {saveConfig.isError && (
            <p role="alert" className="text-destructive">
              {saveConfig.error.message}
            </p>
          )}
        </div>
      </details>
      <JournalSourcePicker
        selected={selected}
        onChange={(names) => {
          setSelected(names);
          setResult(null);
        }}
        disabled={busy}
      />
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          {provider ? `将交给 ${provider.title} 阅读 ${selected.length} 条记录的正文与日期。` : "请先选择并保存服务与模型。"}
        </p>
        {busy ? (
          <div className="flex items-center gap-4">
            <span role="status" className="text-sm">
              正在读这几条记录…
            </span>
            <Button
              variant="ghost"
              onClick={() => {
                controller.current?.abort();
                setBusy(false);
              }}
            >
              取消
            </Button>
          </div>
        ) : (
          <Button disabled={selected.length === 0 || selected.length > 30 || !provider} onClick={generate}>
            开始看看
          </Button>
        )}
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </div>
      {result && (
        <section className="space-y-4 rounded-xl border border-border bg-muted/20 p-6">
          <p className="text-xs tracking-wider text-muted-foreground">AI 洞察 · 一种可能的阅读角度</p>
          <p className="whitespace-pre-wrap leading-8">{result.text}</p>
          <div className="flex flex-wrap gap-3">
            {result.citations.map((name, i) => (
              <Link key={name} className="text-sm underline underline-offset-4" to={`/${name}`}>
                查看来源 {i + 1}
              </Link>
            ))}
          </div>
          <Button variant="ghost" disabled={saved} onClick={save}>
            {saved ? "已保存" : "保存这次洞察"}
          </Button>
          <p className="text-xs text-muted-foreground">未保存的结果会在离开后消失。</p>
        </section>
      )}
      <details onToggle={(e) => setShowHistory(e.currentTarget.open)}>
        <summary className="cursor-pointer py-3 text-sm text-muted-foreground">保存过的洞察</summary>
        {history.isError && <p role="alert">暂时无法读取，请重试。</p>}
        {history.data?.length === 0 && <p className="py-5 text-sm text-muted-foreground">这里还没有保存的洞察。</p>}
        {history.data?.map((item) => (
          <article key={item.id} className="space-y-3 border-t py-5">
            <p className="text-xs text-muted-foreground">AI 洞察{item.stale ? " · 来源已更新" : ""}</p>
            <p className="whitespace-pre-wrap leading-7">{item.text}</p>
            <div className="flex gap-3">
              {item.citations.map((name, i) => (
                <Link className="text-sm underline" key={name} to={`/${name}`}>
                  来源 {i + 1}
                </Link>
              ))}
              <Button
                variant="ghost"
                onClick={async () => {
                  try {
                    await journalRequest(`/insights/${item.id}`, { method: "DELETE" });
                    void history.refetch();
                  } catch (e) {
                    setError(String(e));
                  }
                }}
              >
                删除
              </Button>
            </div>
          </article>
        ))}
      </details>
    </main>
  );
}

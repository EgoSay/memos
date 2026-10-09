import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon, CopyIcon, PlusIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import JournalSharedContent from "@/components/JournalSharedContent";
import JournalSourcePicker from "@/components/JournalSourcePicker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import useCurrentUser from "@/hooks/useCurrentUser";
import { journalRequest, useJournalPreferences } from "@/hooks/useJournalQueries";
import { type JournalShare, type JournalSharedItem, type JournalShareFilter, shareChanges } from "@/lib/journal-share";
import { ROUTES } from "@/router/routes";

interface ShareRequest {
  title: string;
  mode: "fixed" | "dynamic";
  filter: JournalShareFilter;
  excludedMemoNames: string[];
  passcode: string;
  clearPasscode: boolean;
  expiresTs: number;
  version?: number;
  paused: boolean;
}

const toInputTime = (seconds: number) => {
  if (!seconds) return "";
  const value = new Date(seconds * 1000);
  return new Date(value.getTime() - value.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
};

function ShareEditor({
  existing,
  timezone,
  onClose,
  onSaved,
  initialSelection = [],
}: {
  existing?: JournalShare;
  timezone: string;
  onClose: () => void;
  onSaved: () => void;
  initialSelection?: string[];
}) {
  const [title, setTitle] = useState(existing?.title ?? "");
  const [mode, setMode] = useState<"fixed" | "dynamic">(existing?.mode ?? "fixed");
  const [scope, setScope] = useState<"manual" | "filter">(existing && existing.filter.memoNames.length === 0 ? "filter" : "manual");
  const [selected, setSelected] = useState(existing?.filter.memoNames ?? initialSelection);
  const [from, setFrom] = useState(existing?.filter.from ?? "");
  const [to, setTo] = useState(existing?.filter.to ?? "");
  const [tags, setTags] = useState(existing?.filter.tags.join(", ") ?? "");
  const [tagMode, setTagMode] = useState<"any" | "all">(existing?.filter.tagMode ?? "any");
  const [includeSubtags, setIncludeSubtags] = useState(existing?.filter.includeSubtags ?? false);
  const [excluded, setExcluded] = useState(existing?.excludedMemoNames ?? []);
  const [passcode, setPasscode] = useState("");
  const [clearPasscode, setClearPasscode] = useState(false);
  const [expires, setExpires] = useState(toInputTime(existing?.expiresTs ?? 0));
  const [preview, setPreview] = useState<{ key: string; items: JournalSharedItem[]; digest: string }>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const before = useQuery({
    queryKey: ["journal", "share-snapshot", existing?.id, existing?.version],
    queryFn: ({ signal }) => journalRequest<{ share: JournalShare; items: JournalSharedItem[] }>(`/shares/${existing?.id}`, { signal }),
    enabled: Boolean(existing),
    staleTime: 0,
  });
  const request: ShareRequest = {
    title: title.trim() || "一段生活",
    mode,
    filter: {
      from: scope === "filter" ? from : "",
      to: scope === "filter" ? to : "",
      timezone: existing?.filter.timezone ?? timezone,
      tags:
        scope === "filter"
          ? [
              ...new Set(
                tags
                  .split(/[,，]/)
                  .map((tag) => tag.trim().replace(/^#/, ""))
                  .filter(Boolean),
              ),
            ]
          : [],
      tagMode,
      includeSubtags,
      memoNames: scope === "manual" ? selected : [],
    },
    excludedMemoNames: excluded,
    passcode,
    clearPasscode,
    expiresTs: expires ? Math.floor(new Date(expires).getTime() / 1000) : 0,
    version: existing?.version,
    paused: existing?.paused ?? false,
  };
  const key = JSON.stringify(request);
  const previewCurrent = preview?.key === key;
  const diff = useMemo(
    () => (before.data && previewCurrent && preview ? shareChanges(before.data.items, preview.items) : undefined),
    [before.data, preview, previewCurrent],
  );
  const validate = () => {
    if (scope === "manual" && selected.length === 0) return "先选一些想分享的记录。";
    if (scope === "filter" && !from && !to && request.filter.tags.length === 0) return "请选择一个时期或填写标签。";
    if (scope === "filter" && from && to && from > to) return "结束日期不能早于开始日期。";
    if (expires && (!Number.isFinite(request.expiresTs) || request.expiresTs * 1000 <= Date.now())) return "失效时间需要晚于现在。";
    return "";
  };
  const previewShare = async () => {
    setError("");
    const problem = validate();
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    try {
      const value = await journalRequest<{ items: JournalSharedItem[]; previewDigest: string }>("/shares/preview", {
        method: "POST",
        body: key,
      });
      setPreview({ key, items: value.items, digest: value.previewDigest });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "预览未能生成，请重试。");
    } finally {
      setBusy(false);
    }
  };
  const publish = async () => {
    if (!previewCurrent || busy || (existing && !before.isSuccess)) return;
    setBusy(true);
    setError("");
    try {
      await journalRequest(existing ? `/shares/${existing.id}` : "/shares", {
        method: existing ? "PUT" : "POST",
        body: JSON.stringify({ ...request, previewDigest: preview?.digest }),
      });
      setPasscode("");
      onSaved();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "分享未保存，请重试。");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="space-y-7 rounded-xl border border-border/60 bg-card p-5 sm:p-7" aria-label={existing ? "编辑分享" : "创建分享"}>
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-lg font-medium">{existing ? "编辑分享" : "创建分享"}</h2>
        <Button variant="ghost" disabled={busy} onClick={onClose}>
          取消
        </Button>
      </div>
      <fieldset disabled={busy} className="space-y-6">
        <label className="block space-y-2 text-sm">
          <span>分享名称</span>
          <Input value={title} maxLength={120} onChange={(event) => setTitle(event.target.value)} placeholder="一段生活" />
        </label>
        <fieldset className="space-y-3">
          <legend className="mb-2 text-sm">选择范围</legend>
          <div className="flex flex-wrap gap-5 text-sm">
            <label className="flex min-h-11 items-center gap-2">
              <input type="radio" name="share-scope" checked={scope === "manual"} onChange={() => setScope("manual")} />
              自己挑选
            </label>
            <label className="flex min-h-11 items-center gap-2">
              <input type="radio" name="share-scope" checked={scope === "filter"} onChange={() => setScope("filter")} />
              时期或标签
            </label>
          </div>
          {scope === "manual" ? (
            <JournalSourcePicker selected={selected} onChange={setSelected} disabled={busy} allowArchived={false} />
          ) : (
            <div className="space-y-4">
              <div className="grid gap-4 sm:grid-cols-2">
                <label className="block space-y-2 text-sm">
                  <span>开始日期（可留空）</span>
                  <Input type="date" value={from} onChange={(event) => setFrom(event.target.value)} />
                </label>
                <label className="block space-y-2 text-sm">
                  <span>结束日期（含当天，可留空）</span>
                  <Input type="date" value={to} onChange={(event) => setTo(event.target.value)} />
                </label>
              </div>
              <p className="text-xs text-muted-foreground">记录日期时区：{request.filter.timezone}</p>
              <label className="block space-y-2 text-sm">
                <span>标签，用逗号分隔（可留空）</span>
                <Input value={tags} onChange={(event) => setTags(event.target.value)} placeholder="旅行, 日常" />
              </label>
              <div className="flex flex-wrap items-center gap-5 text-sm">
                <label className="flex min-h-11 items-center gap-2">
                  <input type="radio" name="share-tag-mode" checked={tagMode === "any"} onChange={() => setTagMode("any")} />
                  匹配任一标签
                </label>
                <label className="flex min-h-11 items-center gap-2">
                  <input type="radio" name="share-tag-mode" checked={tagMode === "all"} onChange={() => setTagMode("all")} />
                  匹配全部标签
                </label>
              </div>
              <label className="flex min-h-11 items-center gap-2 text-sm">
                <input type="checkbox" checked={includeSubtags} onChange={(event) => setIncludeSubtags(event.target.checked)} />
                包含子标签
              </label>
              <p className="text-sm leading-6 text-muted-foreground">
                同时设置时期和标签时，分享两者都符合的记录。你可以在预览里排除其中几条。
              </p>
            </div>
          )}
        </fieldset>
        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm">后续内容如何变化</legend>
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <input type="radio" name="share-mode" checked={mode === "fixed"} onChange={() => setMode("fixed")} />
            固定快照
          </label>
          <p className="pl-5 text-sm leading-6 text-muted-foreground">保留发布时的内容；修改原文后，只有再次预览并更新分享才会改变。</p>
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <input type="radio" name="share-mode" checked={mode === "dynamic"} onChange={() => setMode("dynamic")} />
            跟随范围更新
          </label>
          <p className="pl-5 text-sm leading-6 text-muted-foreground">
            {scope === "filter"
              ? "以后新增的符合范围的记录也会被看到，原文修改会同步展示。"
              : "只跟随所选记录的内容更新，不会自动加入其他记录。"}
          </p>
        </fieldset>
        <details className="rounded-lg border border-border/60 px-4">
          <summary className="cursor-pointer py-4 text-sm">提取码与有效期</summary>
          <div className="space-y-4 pb-4">
            <label className="block space-y-2 text-sm">
              <span>{existing?.hasPasscode ? "新提取码（留空保留原提取码）" : "提取码（可留空）"}</span>
              <Input
                type="password"
                autoComplete="new-password"
                value={passcode}
                onChange={(event) => {
                  setPasscode(event.target.value);
                  setClearPasscode(false);
                }}
              />
            </label>
            {existing?.hasPasscode && (
              <label className="flex min-h-11 items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={clearPasscode}
                  onChange={(event) => {
                    setClearPasscode(event.target.checked);
                    setPasscode("");
                  }}
                />
                移除原提取码
              </label>
            )}
            <label className="block space-y-2 text-sm">
              <span>失效时间（此设备时间，可留空）</span>
              <Input type="datetime-local" value={expires} onChange={(event) => setExpires(event.target.value)} />
            </label>
          </div>
        </details>
      </fieldset>
      {excluded.length > 0 && (
        <div className="text-sm text-muted-foreground">
          已排除 {excluded.length} 条记录。
          <Button variant="ghost" disabled={busy} onClick={() => setExcluded([])}>
            清空排除
          </Button>
        </div>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      {existing && before.isError && (
        <div role="alert" className="text-sm text-destructive">
          无法读取当前分享，暂不能更新。
          <Button variant="ghost" onClick={() => before.refetch()}>
            重试
          </Button>
        </div>
      )}
      <Button variant="outline" disabled={busy} onClick={() => void previewShare()}>
        {busy ? "正在处理…" : "预览访客会看到的内容"}
      </Button>
      {preview && (
        <section className="space-y-5 border-t border-border pt-6" aria-label="分享预览">
          <h3 className="font-medium">{previewCurrent ? `访客将看到 ${preview.items.length} 条记录` : "范围已修改，请重新预览"}</h3>
          {diff && (
            <p className="text-sm text-muted-foreground">
              相较当前分享：新增 {diff.added} 条，移除 {diff.removed} 条，内容变化 {diff.changed} 条。
            </p>
          )}
          {existing?.mode === "fixed" && (
            <p className="text-sm leading-6 text-muted-foreground">更新会重新保存这次预览中的原文和媒体，覆盖上次快照。</p>
          )}
          {previewCurrent && (
            <>
              <div className="max-h-[36rem] space-y-4 overflow-y-auto">
                {preview.items.map((item, index) => (
                  <div key={item.memoName ?? index} className="space-y-2">
                    <JournalSharedContent items={[item]} timezone={request.filter.timezone} />
                    {item.memoName && (
                      <Button
                        variant="ghost"
                        disabled={busy}
                        onClick={() => setExcluded((previous) => [...new Set([...previous, item.memoName as string])])}
                      >
                        排除这条
                      </Button>
                    )}
                  </div>
                ))}
              </div>
              {preview.items.length === 0 && <p className="text-sm text-muted-foreground">当前范围没有可分享的记录。</p>}
              <div className="space-y-3">
                <p className="text-sm leading-6 text-muted-foreground">
                  持有链接{passcode || (existing?.hasPasscode && !clearPasscode) ? "和提取码" : ""}的人可以查看这些内容。
                </p>
                <Button
                  disabled={busy || preview.items.length === 0 || Boolean(existing && !before.isSuccess)}
                  onClick={() => void publish()}
                >
                  {existing ? "确认更新分享" : "创建分享链接"}
                </Button>
              </div>
            </>
          )}
        </section>
      )}
    </section>
  );
}

export default function JournalShares() {
  const user = useCurrentUser();
  const client = useQueryClient();
  const preferences = useJournalPreferences();
  const [searchParams] = useSearchParams();
  const source = searchParams.get("memo");
  const selectedSource = source && /^memos\/[^/]+$/.test(source) ? source : undefined;
  const [editing, setEditing] = useState<JournalShare | "new" | undefined>(() => (selectedSource ? "new" : undefined));
  const [copied, setCopied] = useState("");
  const [copyError, setCopyError] = useState("");
  const key = ["journal", user?.name, "shares"];
  const shares = useQuery({ queryKey: key, queryFn: ({ signal }) => journalRequest<JournalShare[]>("/shares", { signal }) });
  const change = useMutation({
    mutationFn: ({ share, remove, rotate }: { share: JournalShare; remove?: boolean; rotate?: boolean }) =>
      journalRequest(
        `/shares/${share.id}${rotate ? "/rotate" : ""}`,
        rotate
          ? { method: "POST", body: JSON.stringify({ version: share.version }) }
          : remove
            ? { method: "DELETE" }
            : { method: "PATCH", body: JSON.stringify({ version: share.version, paused: !share.paused }) },
      ),
    onSuccess: () => {
      setCopied("");
      void client.invalidateQueries({ queryKey: key });
    },
  });
  const copy = async (share: JournalShare) => {
    try {
      await navigator.clipboard.writeText(new URL(share.url, window.location.origin).href);
      setCopied(share.id);
      setCopyError("");
    } catch {
      setCopyError("复制未完成，可以直接选中下方链接复制。");
    }
  };
  return (
    <div className="mx-auto min-w-0 max-w-2xl pb-12 pt-2 sm:pt-7 [&_button]:min-h-11">
      <Link to={ROUTES.HOME} className="mb-7 inline-flex min-h-11 items-center gap-2 text-sm text-muted-foreground">
        <ArrowLeftIcon className="size-4" />
        记录
      </Link>
      <header className="mb-8 flex items-center justify-between gap-3">
        <div>
          <h1 className="font-serif text-3xl font-medium tracking-tight">我的分享</h1>
          <p className="mt-3 text-sm text-muted-foreground">把自己选中的片段，分享给想分享的人。</p>
        </div>
        {!editing && (
          <Button variant="outline" onClick={() => setEditing("new")}>
            <PlusIcon className="size-4" />
            新建
          </Button>
        )}
      </header>
      {editing && (
        <div className="mb-8">
          <ShareEditor
            key={editing === "new" ? "new" : `${editing.id}:${editing.version}`}
            existing={editing === "new" ? undefined : editing}
            initialSelection={selectedSource ? [selectedSource] : []}
            timezone={preferences.data?.timezone || "Asia/Shanghai"}
            onClose={() => setEditing(undefined)}
            onSaved={() => {
              setEditing(undefined);
              void shares.refetch();
            }}
          />
        </div>
      )}
      {shares.isPending && (
        <p role="status" className="text-sm text-muted-foreground">
          正在读取分享…
        </p>
      )}
      {shares.isError && (
        <div role="alert">
          <p>分享暂时无法读取。</p>
          <Button variant="outline" onClick={() => shares.refetch()}>
            重试
          </Button>
        </div>
      )}
      {change.isError && (
        <p role="alert" className="mb-4 text-sm text-destructive">
          {change.error.message}
        </p>
      )}
      {copyError && (
        <p role="alert" className="mb-4 text-sm text-muted-foreground">
          {copyError}
        </p>
      )}
      {shares.data?.length === 0 && !editing && (
        <p className="py-10 text-sm text-muted-foreground">还没有创建分享。原始记录始终留在自己的空间。</p>
      )}
      <div className="space-y-4">
        {shares.data?.map((share) => (
          <article key={share.id} className="rounded-xl border border-border/60 bg-card p-5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="min-w-0 break-words font-medium">{share.title}</h2>
              <span className="text-xs text-muted-foreground">
                {share.paused
                  ? "已停用"
                  : share.expiresTs && share.expiresTs * 1000 <= Date.now()
                    ? "已到期"
                    : share.mode === "fixed"
                      ? "固定快照"
                      : "跟随范围更新"}
              </span>
            </div>
            <a
              href={share.url}
              target="_blank"
              rel="noreferrer"
              className="mt-3 block break-all text-sm text-muted-foreground underline underline-offset-4"
            >
              {new URL(share.url, window.location.origin).href}
            </a>
            <div className="mt-4 flex flex-wrap gap-2">
              <Button variant="ghost" onClick={() => void copy(share)}>
                <CopyIcon className="size-4" />
                {copied === share.id ? "已复制" : "复制链接"}
              </Button>
              <Button variant="ghost" disabled={change.isPending} onClick={() => setEditing(share)}>
                编辑与更新
              </Button>
              <Button variant="ghost" disabled={change.isPending} onClick={() => change.mutate({ share })}>
                {share.paused ? "恢复分享" : "停用"}
              </Button>
              <Button
                variant="ghost"
                disabled={change.isPending}
                onClick={() => {
                  if (window.confirm("重新生成链接后，旧链接会立即失效。继续吗？")) change.mutate({ share, rotate: true });
                }}
              >
                重新生成链接
              </Button>
              <Button
                variant="ghost"
                disabled={change.isPending}
                onClick={() => {
                  if (window.confirm("撤销这个分享链接？原始记录会保留，链接将无法继续访问。")) change.mutate({ share, remove: true });
                }}
              >
                撤销链接
              </Button>
            </div>
          </article>
        ))}
      </div>
    </div>
  );
}

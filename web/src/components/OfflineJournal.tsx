import { useEffect, useRef, useState } from "react";
import { createInitialState, type EditorState } from "@/components/MemoEditor/state";
import type { DeviceOwner } from "@/lib/journal-device";
import { type JournalDraft, journalDrafts, restoreJournalState } from "@/lib/journal-drafts";

/** A cold offline start reads device drafts, without pretending to have a verified session. */
export default function OfflineJournal({ owner }: { owner?: DeviceOwner }) {
  const [state, setState] = useState<EditorState>(createInitialState);
  const [key, setKey] = useState(owner ? `offline:${owner.name}` : "");
  const [options, setOptions] = useState<JournalDraft["options"]>();
  const [entries, setEntries] = useState<JournalDraft[]>([]);
  const [status, setStatus] = useState("正在读取此设备上的草稿…");
  const [error, setError] = useState("");
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [online, setOnline] = useState(navigator.onLine);
  const urls = useRef(new Set<string>());
  const generation = useRef(0);
  const stateRef = useRef(state);
  stateRef.current = state;
  useEffect(() => {
    const update = () => setOnline(navigator.onLine);
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    return () => {
      window.removeEventListener("online", update);
      window.removeEventListener("offline", update);
    };
  }, []);
  useEffect(
    () => () => {
      for (const url of urls.current) URL.revokeObjectURL(url);
    },
    [],
  );
  useEffect(() => {
    if (!owner) return;
    let alive = true;
    void journalDrafts
      .list(owner.name)
      .then((drafts) => {
        if (!alive) return;
        setEntries(drafts);
        const draft = drafts.find((d) => d.key === `offline:${owner.name}` && d.kind === "draft");
        if (draft) {
          const restored = restoreJournalState(draft);
          for (const file of restored.localFiles) urls.current.add(file.previewUrl);
          setState(restored);
        }
        setReady(true);
        setStatus("草稿保存在此设备");
      })
      .catch(() => {
        if (alive) setStatus("本地草稿暂时无法读取，请保留原文件并检查浏览器存储权限。");
      });
    return () => {
      alive = false;
    };
  }, [owner]);
  useEffect(() => {
    if (!owner || !ready || busy) return;
    const current = ++generation.current;
    setStatus("正在保存草稿…");
    void journalDrafts
      .save(key, owner.name, state, options)
      .then(() => {
        if (generation.current === current) setStatus("草稿已保存在此设备");
      })
      .catch(() => {
        if (generation.current === current) setStatus("草稿保存失败，请复制文字并保留原文件。");
      });
  }, [owner, ready, busy, state, key, options]);
  const refresh = async () => {
    if (owner) setEntries(await journalDrafts.list(owner.name));
  };
  const load = async (draft: JournalDraft) => {
    if (!owner || busy) return;
    setError("");
    setBusy(true);
    try {
      await journalDrafts.save(key, owner.name, stateRef.current, options);
      const restored = restoreJournalState(draft);
      for (const file of restored.localFiles) urls.current.add(file.previewUrl);
      setState(restored);
      setKey(draft.key);
      setOptions(draft.options);
    } catch {
      setError("切换草稿前未能完成保存，当前文字和文件仍保留在这里。请重试。");
    } finally {
      setBusy(false);
    }
  };
  const save = async () => {
    if (!owner || busy) return;
    setError("");
    setBusy(true);
    ++generation.current;
    try {
      const current = stateRef.current;
      await journalDrafts.save(`pending:${owner.name}:${current.clientId}`, owner.name, current, options, "pending");
      await journalDrafts.remove(key);
      setState(createInitialState());
      setKey(`offline:${owner.name}`);
      setOptions(undefined);
      await refresh();
      setStatus("记录已保存在此设备，联网登录后会继续同步。");
    } catch {
      setError("保存未完成，文字和文件仍在当前页面。请重试。");
    } finally {
      setBusy(false);
    }
  };
  const reconnect = async () => {
    setError("");
    if (owner && ready) {
      try {
        await journalDrafts.save(key, owner.name, stateRef.current, options);
      } catch {
        setError("草稿未保存，请先复制文字和保留原文件。");
        return;
      }
    }
    location.reload();
  };
  return (
    <main className="max-w-2xl mx-auto px-5 py-10 space-y-6">
      <header className="space-y-2">
        <h1 className="text-xl font-medium">此设备上的记录</h1>
        <p className="text-sm text-muted-foreground">当前处于离线模式。这里的文字和附件只保存在这个浏览器中。</p>
      </header>
      {!owner ? (
        <p>请先联网登录一次，之后便可在断网时继续记录。已退出登录的本地记录不会在此显示。</p>
      ) : (
        <>
          <p className="text-sm text-muted-foreground">本地记录归属：{owner.username}。连接服务后会重新验证登录。</p>
          <textarea
            aria-label="离线记录内容"
            placeholder="写点什么…"
            value={state.content}
            disabled={!ready || busy}
            onChange={(event) => {
              setError("");
              setState((previous) => ({ ...previous, content: event.target.value }));
            }}
            className="w-full min-h-48 bg-transparent border border-border rounded-md p-4 outline-none focus:ring-1 focus:ring-ring"
          />
          <div className="space-y-2">
            {state.localFiles.map((item) => (
              <div key={item.clientId ?? item.previewUrl} className="flex justify-between gap-3 text-sm">
                <a href={item.previewUrl} download={item.file.name} className="underline break-all">
                  {item.file.name}
                </a>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => setState((previous) => ({ ...previous, localFiles: previous.localFiles.filter((file) => file !== item) }))}
                >
                  移除
                </button>
              </div>
            ))}
          </div>
          <div className="flex flex-wrap items-center justify-between gap-4">
            <label className="text-sm cursor-pointer underline">
              添加照片或文件
              <input
                type="file"
                className="sr-only"
                multiple
                disabled={!ready || busy}
                onChange={(event) => {
                  const files = Array.from(event.target.files ?? []).map((file) => {
                    const previewUrl = URL.createObjectURL(file);
                    urls.current.add(previewUrl);
                    return { file, previewUrl, clientId: crypto.randomUUID().replaceAll("-", ""), origin: "upload" as const };
                  });
                  setState((previous) => ({ ...previous, localFiles: [...previous.localFiles, ...files] }));
                  event.target.value = "";
                }}
              />
            </label>
            <button
              type="button"
              className="bg-primary text-primary-foreground rounded-md px-4 py-2 disabled:opacity-50"
              disabled={!ready || busy || (!state.content.trim() && !state.localFiles.length && !state.metadata.attachments.length)}
              onClick={() => void save()}
            >
              保存到此设备
            </button>
          </div>
          <p role="status" className="text-sm text-muted-foreground">
            {status}
          </p>
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          {entries.length > 0 && (
            <details>
              <summary className="cursor-pointer py-3 text-sm">本地草稿与待同步记录</summary>
              <div className="space-y-3">
                {entries
                  .filter((entry) => entry.key !== key)
                  .map((entry) => (
                    <article key={entry.key} className="border-b border-border py-3 text-sm space-y-2">
                      <p className="whitespace-pre-wrap break-words">{entry.state.content || "照片或录音记录"}</p>
                      <p className="text-muted-foreground">
                        {entry.kind === "pending" ? "已保存，等待同步" : "草稿"} · {entry.state.localFiles.length} 个附件
                      </p>
                      {entry.kind === "draft" && (
                        <button type="button" className="underline" disabled={busy} onClick={() => void load(entry)}>
                          继续写
                        </button>
                      )}
                    </article>
                  ))}
              </div>
            </details>
          )}
        </>
      )}
      <button type="button" className="text-sm underline min-h-11" disabled={busy} onClick={() => void reconnect()}>
        {online ? "重新连接并登录" : "尝试重新连接"}
      </button>
    </main>
  );
}

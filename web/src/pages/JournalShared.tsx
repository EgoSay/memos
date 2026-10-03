import { useCallback, useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import JournalSharedContent from "@/components/JournalSharedContent";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { JournalSharedItem } from "@/lib/journal-share";

interface SharedCollection {
  title: string;
  items: JournalSharedItem[];
  timezone?: string;
}

export default function JournalShared() {
  const { token = "" } = useParams();
  const [passcode, setPasscode] = useState("");
  const [collection, setCollection] = useState<SharedCollection>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const code = useRef("");
  const currentRequest = useRef<AbortController | undefined>(undefined);
  const open = useCallback(
    async (value: string) => {
      currentRequest.current?.abort();
      const controller = new AbortController();
      currentRequest.current = controller;
      setCollection(undefined);
      setBusy(true);
      setError("");
      try {
        const response = await fetch(`/api/v1/journal-shares/${encodeURIComponent(token)}`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ passcode: value }),
          credentials: "omit",
          cache: "no-store",
          referrerPolicy: "no-referrer",
          signal: controller.signal,
        });
        if (!response.ok) throw new Error("这个分享暂时无法打开。若有提取码，可以在下方填写后重试。");
        const data = (await response.json()) as SharedCollection;
        if (!controller.signal.aborted) {
          code.current = value;
          setCollection(data);
        }
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : "暂时无法打开分享，请重试。");
      } finally {
        if (currentRequest.current === controller) setBusy(false);
      }
    },
    [token],
  );
  useEffect(() => {
    code.current = "";
    void open("");
    const onVisible = () => {
      if (document.visibilityState === "visible") void open(code.current);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      currentRequest.current?.abort();
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [open]);
  useEffect(() => {
    const robots = document.createElement("meta");
    robots.name = "robots";
    robots.content = "noindex, nofollow, noarchive";
    const referrer = document.createElement("meta");
    referrer.name = "referrer";
    referrer.content = "no-referrer";
    document.head.append(robots, referrer);
    return () => {
      robots.remove();
      referrer.remove();
    };
  }, []);
  return (
    <main className="min-h-dvh bg-background px-5 py-12 text-foreground sm:py-20 [&_button]:min-h-11">
      <div className="mx-auto max-w-2xl">
        <header className="mb-10">
          <p className="mb-4 text-xs tracking-widest text-muted-foreground">生活片段</p>
          <h1 className="break-words font-serif text-3xl font-medium tracking-tight">{collection?.title || "分享的记录"}</h1>
        </header>
        {busy && (
          <p role="status" className="text-sm text-muted-foreground">
            正在打开分享…
          </p>
        )}
        {error && (
          <div className="max-w-sm space-y-5">
            <p role="alert" className="text-sm leading-6 text-muted-foreground">
              {error}
            </p>
            <form
              className="space-y-4"
              onSubmit={(event) => {
                event.preventDefault();
                void open(passcode);
              }}
            >
              <label className="block space-y-2 text-sm">
                <span>提取码</span>
                <Input type="password" autoComplete="off" value={passcode} onChange={(event) => setPasscode(event.target.value)} />
              </label>
              <Button type="submit" disabled={busy}>
                打开分享
              </Button>
            </form>
          </div>
        )}
        {collection && (
          <>
            {collection.items.length === 0 ? (
              <p className="text-sm text-muted-foreground">这里暂时没有可查看的记录。</p>
            ) : (
              <JournalSharedContent items={collection.items} timezone={collection.timezone} />
            )}
          </>
        )}
      </div>
    </main>
  );
}

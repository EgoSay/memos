import { useMemo } from "react";
import { type JournalSharedItem, sharedMediaKind } from "@/lib/journal-share";

/** Deliberately inert text: markdown links, HTML, embeds and remote media cannot fetch private or external URLs. */
export default function JournalSharedContent({ items, timezone = "Asia/Shanghai" }: { items: JournalSharedItem[]; timezone?: string }) {
  const normalizedItems = useMemo(() => items ?? [], [items]);
  return (
    <div className="space-y-6">
      {normalizedItems.map((item, index) => (
        <article key={item.memoName ?? `${item.createdTs}-${index}`} className="rounded-xl border border-border/60 bg-card p-5 sm:p-7">
          <time dateTime={new Date(item.createdTs * 1000).toISOString()} className="mb-4 block text-sm text-muted-foreground">
            {new Date(item.createdTs * 1000).toLocaleString("zh-CN", { timeZone: timezone, dateStyle: "long", timeStyle: "short" })}
          </time>
          {item.content && <p className="whitespace-pre-wrap break-words text-base leading-7">{item.content}</p>}
          <div className="mt-4 space-y-4">
            {(item.media ?? []).map((media) => {
              const kind = sharedMediaKind(media.type);
              const source = `data:${media.type};base64,${media.content}`;
              if (kind === "image")
                return (
                  <img
                    key={media.id}
                    src={source}
                    alt={media.filename || "分享的照片"}
                    loading="lazy"
                    className="max-h-[36rem] max-w-full rounded-lg object-contain"
                  />
                );
              if (kind === "audio")
                return (
                  <figure key={media.id} className="space-y-2">
                    <figcaption className="break-words text-sm text-muted-foreground">{media.filename || "分享的声音"}</figcaption>
                    <audio controls preload="none" src={source} className="max-w-full">
                      <track kind="captions" />
                    </audio>
                  </figure>
                );
              return (
                <p key={media.id} className="break-words text-sm text-muted-foreground">
                  {media.filename}（此格式暂不预览）
                </p>
              );
            })}
          </div>
        </article>
      ))}
    </div>
  );
}

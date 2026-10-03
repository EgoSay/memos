export interface JournalShareFilter {
  from: string;
  to: string;
  timezone: string;
  tags: string[];
  tagMode: "any" | "all";
  includeSubtags: boolean;
  memoNames: string[];
}

export interface JournalShare {
  id: string;
  title: string;
  mode: "fixed" | "dynamic";
  filter: JournalShareFilter;
  excludedMemoNames: string[];
  expiresTs: number;
  createdTs: number;
  paused: boolean;
  version: number;
  hasPasscode: boolean;
  url: string;
}

export interface JournalSharedMedia {
  id: string;
  filename: string;
  type: string;
  content: string;
}

export interface JournalSharedItem {
  memoName?: string;
  content: string;
  createdTs: number;
  media: JournalSharedMedia[];
}

/** Only authorized inert formats are embedded; SVG/HTML and arbitrary data URLs are never accepted. */
export function sharedMediaKind(type: string): "image" | "audio" | undefined {
  if (["image/jpeg", "image/png", "image/webp", "image/gif", "image/avif"].includes(type)) return "image";
  if (["audio/mpeg", "audio/mp3", "audio/mp4", "audio/wav", "audio/x-wav", "audio/ogg", "audio/webm"].includes(type)) return "audio";
  return undefined;
}

/** Preview changes compare the actual previous snapshot, not a fresh evaluation of its old filter. */
export function shareChanges(previous: readonly JournalSharedItem[], next: readonly JournalSharedItem[]) {
  const previousByName = new Map(previous.filter((item) => item.memoName).map((item) => [item.memoName, item]));
  const nextByName = new Map(next.filter((item) => item.memoName).map((item) => [item.memoName, item]));
  let added = 0;
  let changed = 0;
  for (const [name, item] of nextByName) {
    const old = previousByName.get(name);
    if (!old) added++;
    else if (old.content !== item.content || old.createdTs !== item.createdTs || JSON.stringify(old.media) !== JSON.stringify(item.media))
      changed++;
  }
  return { added, removed: [...previousByName.keys()].filter((name) => !nextByName.has(name)).length, changed };
}

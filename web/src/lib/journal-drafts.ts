import type { EditorState } from "@/components/MemoEditor/state";
import type { LocalFile } from "@/components/MemoEditor/types/attachment";
import type { MediaMetadata } from "@/types/proto/api/v1/attachment_service_pb";

const DATABASE = "memos-personal-journal";
const STORE = "drafts";

export type StoredLocalFile = Omit<LocalFile, "previewUrl" | "mediaMetadata"> & { mediaMetadata?: MediaMetadata };
export interface JournalDraft {
  key: string;
  owner: string;
  kind: "draft" | "pending";
  state: Omit<EditorState, "localFiles"> & { localFiles: StoredLocalFile[] };
  options?: { memoName?: string; parentMemoName?: string; space?: string };
  updatedAt: number;
  error?: string;
}

// Serialize writes per key: slow metadata extraction must never let an old
// keystroke overwrite a newer draft or resurrect a successfully saved draft.
const writes = new Map<string, Promise<unknown>>();
function ordered<T>(key: string, action: () => Promise<T>): Promise<T> {
  const previous = writes.get(key) ?? Promise.resolve();
  const next = previous.catch(() => undefined).then(action);
  writes.set(key, next);
  void next
    .finally(() => {
      if (writes.get(key) === next) writes.delete(key);
    })
    .catch(() => undefined);
  return next;
}

async function database(): Promise<IDBDatabase> {
  if (typeof indexedDB === "undefined") throw new Error("当前浏览器无法持久保存草稿，请先复制文字并保留原文件。");
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE, 1);
    request.onupgradeneeded = () => request.result.createObjectStore(STORE, { keyPath: "key" });
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
    request.onblocked = () => reject(new Error("本地存储被其他页面占用，请关闭旧页面后重试。"));
  });
}

async function transaction<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await database();
  try {
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction(STORE, mode);
      const request = action(tx.objectStore(STORE));
      tx.oncomplete = () => resolve(request.result);
      tx.onerror = () => reject(tx.error ?? request.error);
      tx.onabort = () => reject(tx.error ?? new Error("本地保存未完成，输入仍保留在当前页面。"));
    });
  } finally {
    db.close();
  }
}

export async function snapshotJournalState(state: EditorState): Promise<JournalDraft["state"]> {
  return {
    ...state,
    localFiles: await Promise.all(
      state.localFiles.map(async ({ previewUrl: _previewUrl, mediaMetadata, ...file }) => ({
        ...file,
        mediaMetadata: await mediaMetadata?.catch(() => undefined),
      })),
    ),
  };
}

export function restoreJournalState(draft: JournalDraft): EditorState {
  return {
    ...draft.state,
    ui: {
      ...draft.state.ui,
      justSaved: false,
      pendingInlineImageInsertions: 0,
      isLoading: { saving: false, uploading: false, loading: false, transcribing: false },
    },
    recorderBusy: false,
    localFiles: draft.state.localFiles.map(({ mediaMetadata, ...file }) => ({
      ...file,
      previewUrl: URL.createObjectURL(file.file),
      mediaMetadata: mediaMetadata ? Promise.resolve(mediaMetadata) : undefined,
    })),
  };
}

export const journalDrafts = {
  save(key: string, owner: string, state: EditorState, options?: JournalDraft["options"], kind: JournalDraft["kind"] = "draft") {
    return ordered(key, async () => {
      const entry: JournalDraft = { key, owner, kind, options, state: await snapshotJournalState(state), updatedAt: Date.now() };
      await transaction("readwrite", (store) => store.put(entry));
      window.dispatchEvent(new CustomEvent("journal-drafts-changed", { detail: { key, kind } }));
      return entry;
    });
  },
  async get(key: string): Promise<JournalDraft | undefined> {
    await writes.get(key)?.catch(() => undefined);
    return transaction("readonly", (store) => store.get(key));
  },
  async list(owner: string): Promise<JournalDraft[]> {
    const entries = await transaction<JournalDraft[]>("readonly", (store) => store.getAll());
    return entries.filter((entry) => entry.owner === owner).sort((a, b) => a.updatedAt - b.updatedAt);
  },
  async pending(owner: string): Promise<JournalDraft[]> {
    const entries = await transaction<JournalDraft[]>("readonly", (store) => store.getAll());
    return entries.filter((entry) => entry.owner === owner && entry.kind === "pending").sort((a, b) => a.updatedAt - b.updatedAt);
  },
  remove(key: string) {
    return ordered(key, async () => {
      await transaction("readwrite", (store) => store.delete(key));
      window.dispatchEvent(new CustomEvent("journal-drafts-changed", { detail: { key } }));
    });
  },
};

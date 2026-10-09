import { useCallback, useEffect, useRef, useState } from "react";
import { journalDrafts } from "@/lib/journal-drafts";
import { useEditorStore } from "../state";

/** Writes bytes as well as text; a success label means an IndexedDB commit. */
export function useDurableDraft(owner: string, key: string, enabled: boolean) {
  const store = useEditorStore();
  const [status, setStatus] = useState<"idle" | "saving" | "saved" | "failed">("idle");
  const discarded = useRef<string | undefined>(undefined);
  const generation = useRef(0);
  useEffect(() => {
    if (!enabled) return;
    let active = true;
    let previous = store.getState();
    const persist = () => {
      const state = store.getState();
      if (state.clientId === discarded.current) return;
      const current = ++generation.current;
      if (!state.content && !state.localFiles.length && !state.metadata.attachments.length) {
        void journalDrafts
          .remove(key)
          .then(() => {
            if (active && generation.current === current) setStatus("idle");
          })
          .catch(() => {
            if (active && generation.current === current) setStatus("failed");
          });
        return;
      }
      setStatus("saving");
      void journalDrafts
        .save(key, owner, state, { memoName: state.baselineMemo?.name })
        .then(() => {
          if (active && generation.current === current) setStatus("saved");
        })
        .catch(() => {
          if (active && generation.current === current) setStatus("failed");
        });
    };
    persist();
    const unsubscribe = store.subscribe(() => {
      const next = store.getState();
      if (
        next.content !== previous.content ||
        next.metadata !== previous.metadata ||
        next.localFiles !== previous.localFiles ||
        next.timestamps !== previous.timestamps ||
        next.clientId !== previous.clientId ||
        next.baselineMemo !== previous.baselineMemo
      )
        persist();
      previous = next;
    });
    return () => {
      active = false;
      unsubscribe();
    };
  }, [enabled, key, owner, store]);
  const discard = useCallback(() => {
    discarded.current = store.getState().clientId;
    ++generation.current;
    setStatus("idle");
    void journalDrafts.remove(key).catch(() => setStatus("failed"));
  }, [key, store]);
  return { status, discard };
}

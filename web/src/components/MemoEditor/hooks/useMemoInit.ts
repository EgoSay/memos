import { useEffect, useRef, useState } from "react";
import { getMemoPartition } from "@/hooks/useJournalPartitionQueries";
import { journalDrafts, restoreJournalState } from "@/lib/journal-drafts";
import type { Location, Memo, Visibility } from "@/types/proto/api/v1/memo_service_pb";
import { cacheService, memoService } from "../services";
import { useEditorContext } from "../state";
import type { EditorController } from "../types/editorController";

interface UseMemoInitOptions {
  editorRef: React.RefObject<EditorController | null>;
  memo?: Memo;
  cacheKey?: string;
  username: string;
  autoFocus?: boolean | (() => boolean);
  defaultVisibility?: Visibility;
  defaultCreateTime?: Date;
  defaultLocation?: Location;
  canChooseSpace?: boolean;
}

export const useMemoInit = ({
  editorRef,
  memo,
  cacheKey,
  username,
  autoFocus,
  defaultVisibility,
  defaultCreateTime,
  defaultLocation,
  canChooseSpace = false,
}: UseMemoInitOptions) => {
  const { actions, dispatch, getState } = useEditorContext();
  const initializedRef = useRef(false);
  const [isInitialized, setIsInitialized] = useState(false);

  useEffect(() => {
    if (initializedRef.current) return;
    initializedRef.current = true;
    const key = cacheService.key(username, cacheKey);

    if (memo) {
      const initialState = memoService.fromMemo(memo);
      dispatch(actions.initMemo(initialState));
      void getMemoPartition(memo.name)
        .then((mapping) => {
          if (getState().metadata.journalPartitionId === undefined)
            dispatch(
              actions.setMetadata({ journalPartitionId: mapping.partitionId, journalPartitionSuspended: mapping.suspended ?? false }),
            );
        })
        .catch(() => undefined);
    } else {
      const cachedDraft = cacheService.loadDraft(key);
      if (cachedDraft.content) {
        dispatch(actions.setContent(cachedDraft.content));
      }
      if (cachedDraft.attachments.length > 0) {
        dispatch(actions.setMetadata({ attachments: cachedDraft.attachments }));
      }
      dispatch(actions.setMetadata({ location: cachedDraft.location === null ? undefined : (cachedDraft.location ?? defaultLocation) }));
      const visibility = cachedDraft.visibility ?? defaultVisibility;
      if (visibility !== undefined) {
        dispatch(actions.setMetadata({ visibility }));
      }
      if (canChooseSpace) {
        dispatch(actions.setMetadata({ space: cachedDraft.space }));
      }
      if (defaultCreateTime) {
        dispatch(actions.setTimestamps({ createTime: defaultCreateTime, updateTime: defaultCreateTime }));
      }
    }

    const cachedCursor = cacheService.loadCursor(key);
    let restoreCursorTimer: ReturnType<typeof setTimeout> | undefined;
    if (autoFocus || cachedCursor !== undefined) {
      restoreCursorTimer = setTimeout(() => {
        if (cachedCursor !== undefined) {
          editorRef.current?.setCursor(cachedCursor);
        }
        if (typeof autoFocus === "function" ? autoFocus() : autoFocus) {
          editorRef.current?.focus();
        }
      }, 100);
    }

    // The durable copy includes File bytes and the original edit baseline.
    // Prefer it only before editing begins; initialization gates autosave.
    if (typeof indexedDB === "undefined") setIsInitialized(true);
    else
      void journalDrafts
        .get(key)
        .then((draft) => {
          if (draft?.owner === username) dispatch(actions.restoreDraft(restoreJournalState(draft)));
        })
        .catch(() => undefined)
        .finally(() => setIsInitialized(true));
    return () => {
      if (restoreCursorTimer) {
        clearTimeout(restoreCursorTimer);
      }
    };
  }, [
    memo,
    cacheKey,
    username,
    autoFocus,
    defaultVisibility,
    defaultCreateTime,
    defaultLocation,
    canChooseSpace,
    actions,
    dispatch,
    editorRef,
    getState,
  ]);

  return { isInitialized };
};

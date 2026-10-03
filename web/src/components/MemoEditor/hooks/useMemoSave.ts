import { Code, ConnectError } from "@connectrpc/connect";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { toast } from "react-hot-toast";
import { memoServiceClient } from "@/connect";
import { useNewMemo } from "@/contexts/NewMemoContext";
import { attachmentKeys } from "@/hooks/useAttachmentQueries";
import { memoKeys } from "@/hooks/useMemoQueries";
import { userKeys } from "@/hooks/useUserQueries";
import { handleError } from "@/lib/error";
import { journalDrafts } from "@/lib/journal-drafts";
import type { Memo, Visibility } from "@/types/proto/api/v1/memo_service_pb";
import { useTranslate } from "@/utils/i18n";
import { errorService, memoService, validationService } from "../services";
import { useEditorContext } from "../state";

/** How long a closing host shows "Saved" before it unmounts the editor. */
const SAVED_CONFIRMATION_MS = 900;

interface UseMemoSaveOptions {
  owner?: string;
  memoName?: string;
  parentMemoName?: string;
  defaultSpace?: string;
  defaultVisibility?: Visibility;
  defaultCreateTime?: Date;
  discardDraft: () => void;
  onConfirm?: (memoName: string) => void;
  onConflict?: (latest: Memo) => void;
  onCancel?: () => void;
}

/**
 * Owns the editor's save transaction and its post-save cache/state updates.
 * Keeping this workflow outside the shell makes saving identical whether it is
 * triggered by the toolbar or the editor keyboard shortcut.
 */
export function useMemoSave({
  owner,
  memoName,
  parentMemoName,
  defaultSpace,
  defaultVisibility,
  defaultCreateTime,
  discardDraft,
  onConfirm,
  onConflict,
  onCancel,
}: UseMemoSaveOptions): () => Promise<void> {
  const t = useTranslate();
  const queryClient = useQueryClient();
  const { markNewMemo } = useNewMemo();
  const { actions, dispatch, getState } = useEditorContext();

  return useCallback(async () => {
    const state = getState();
    // A repeated shortcut during the saved confirmation is not an error worth
    // a toast; the save already landed and the host is closing.
    if (state.ui.justSaved) return;
    const { valid, reason, detail } = validationService.canSave(state);
    if (!valid) {
      toast.error(reason ? t(reason, detail ? { url: detail } : undefined) : t("editor.validation.cannot-save"));
      return;
    }

    dispatch(actions.setLoading("saving", true));

    try {
      const result = await memoService.save(state, { memoName, parentMemoName, space: defaultSpace });

      if (!result.hasChanges) {
        toast.error(t("editor.no-changes-detected"));
        onCancel?.();
        return;
      }

      if (result.syncError) toast.error(result.syncError, { duration: 7000 });

      // Prevent the autosave unmount flush from restoring the saved draft.
      discardDraft();
      // A moved memo may leave the feed it was edited in; say where it went.
      if (result.moved) {
        toast.success(t("memo.moved"));
      }

      const invalidationPromises: Promise<unknown>[] = [
        queryClient.invalidateQueries({ queryKey: memoKeys.lists() }),
        queryClient.invalidateQueries({ queryKey: userKeys.stats() }),
        queryClient.invalidateQueries({ queryKey: attachmentKeys.lists() }),
      ];
      if (memoName) {
        invalidationPromises.push(queryClient.invalidateQueries({ queryKey: memoKeys.detail(memoName) }));
      }
      if (parentMemoName) {
        invalidationPromises.push(queryClient.invalidateQueries({ queryKey: memoKeys.comments(parentMemoName) }));
        invalidationPromises.push(queryClient.invalidateQueries({ queryKey: memoKeys.detail(parentMemoName) }));
      }
      // Hosts that close after saving (edit, comment) hold a brief "Saved"
      // confirmation on the toolbar while the caches refresh underneath. The
      // in-place composer clears immediately, so it shows nothing.
      if (memoName || parentMemoName) {
        dispatch(actions.setLoading("saving", false));
        dispatch(actions.setJustSaved(true));
        invalidationPromises.push(new Promise((resolve) => setTimeout(resolve, SAVED_CONFIRMATION_MS)));
      }
      await Promise.all(invalidationPromises);

      dispatch(actions.reset());
      if (!memoName && defaultVisibility) {
        dispatch(actions.setMetadata({ visibility: defaultVisibility }));
      }
      // Reset creates a fresh editor state, so restore calendar-derived values
      // for the next memo created without remounting this composer.
      if (!memoName && defaultCreateTime) {
        dispatch(actions.setTimestamps({ createTime: defaultCreateTime, updateTime: defaultCreateTime }));
      }

      if (!memoName && !parentMemoName) {
        markNewMemo(result.memoName);
      }
      onConfirm?.(result.memoName);
    } catch (error) {
      const code = ConnectError.from(error).code;
      if (code === Code.Aborted && memoName && onConflict) {
        try {
          onConflict(await memoServiceClient.getMemo({ name: memoName }));
        } catch {
          // The current editor and durable draft remain intact even when the
          // latest version cannot be fetched for comparison.
        }
      }
      if (
        owner &&
        state.clientId &&
        !parentMemoName &&
        (!navigator.onLine || code === Code.Unavailable || code === Code.DeadlineExceeded)
      ) {
        try {
          await journalDrafts.save(`pending:${owner}:${state.clientId}`, owner, state, { memoName, space: defaultSpace }, "pending");
          discardDraft();
          dispatch(actions.reset());
          toast.success("已保存在此设备，联网后同步。正文和原文件会一起保留。");
          onCancel?.();
          return;
        } catch {
          toast.error("此设备未能保存，输入仍在页面中。请复制文字并保留原文件后重试。");
          return;
        }
      }
      handleError(error, toast.error, {
        context: "Failed to save memo",
        fallbackMessage: errorService.getErrorMessage(error),
      });
    } finally {
      dispatch(actions.setLoading("saving", false));
      dispatch(actions.setJustSaved(false));
    }
  }, [
    actions,
    owner,
    defaultCreateTime,
    defaultSpace,
    defaultVisibility,
    discardDraft,
    dispatch,
    getState,
    markNewMemo,
    memoName,
    onCancel,
    onConfirm,
    onConflict,
    parentMemoName,
    queryClient,
    t,
  ]);
}

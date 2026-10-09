import { CheckIcon, LoaderIcon } from "lucide-react";
import { type FC, useCallback } from "react";
import PartitionPicker from "@/components/Journal/PartitionPicker";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { type Location, Visibility } from "@/types/proto/api/v1/memo_service_pb";
import { useTranslate } from "@/utils/i18n";
import { primaryModifierGlyph } from "@/utils/platform";
import { validationService } from "../services";
import { useEditorContext, useEditorSelector } from "../state";
import type { EditorToolbarProps } from "../types";
import AudienceMenu from "./AudienceMenu";
import InsertMenu from "./InsertMenu";

export const EditorToolbar: FC<EditorToolbarProps> = ({
  onSave,
  onCancel,
  memoName,
  parentMemoName,
  space,
  canChooseSpace,
  onAudioRecorderClick,
  viewToggles,
  onInsertImages,
  onInsertTag,
}) => {
  const t = useTranslate();
  const { actions, dispatch } = useEditorContext();
  // Subscribe to narrow/derived slices so typing (which only changes content)
  // doesn't re-render the toolbar or the heavy InsertMenu it hosts. `valid`
  // flips only on empty↔non-empty / loading transitions, not per keystroke.
  const valid = useEditorSelector((s) => validationService.canSave(s).valid);
  const blockedReason = useEditorSelector((s) => validationService.canSave(s).reason);
  const blockedReasonDetail = useEditorSelector((s) => validationService.canSave(s).detail);
  const isSaving = useEditorSelector((s) => s.ui.isLoading.saving);
  const justSaved = useEditorSelector((s) => s.ui.justSaved);
  const isUploading = useEditorSelector((s) => s.ui.isLoading.uploading);
  const location = useEditorSelector((s) => s.metadata.location);
  const partitionId = useEditorSelector((s) => s.metadata.journalPartitionId ?? "");
  const suspended = useEditorSelector((s) => s.metadata.journalPartitionSuspended ?? false);
  const syncEnabled = useEditorSelector((s) => s.metadata.journalSyncEnabled ?? false);
  const syncChange = useCallback((enabled: boolean) => dispatch(actions.setMetadata({ journalSyncEnabled: enabled })), [actions, dispatch]);
  const visibility = useEditorSelector((s) => s.metadata.visibility);
  // The save transaction is in flight or its confirmation is holding the
  // editor open; either way the toolbar is frozen.
  const committing = isSaving || justSaved;
  const blockedMessage =
    valid || committing
      ? undefined
      : blockedReason
        ? t(blockedReason, blockedReasonDetail ? { url: blockedReasonDetail } : undefined)
        : t("editor.validation.cannot-save");
  // The verb names what the host does with the memo: an existing memo is
  // updated, a reply becomes a comment, and a new memo is simply saved. A memo
  // is stored with a visibility, not posted, so messaging verbs stay out.
  const commitLabel = syncEnabled ? "保存并同步" : memoName ? t("common.update") : parentMemoName ? t("editor.comment") : t("editor.save");

  const handleLocationChange = (next?: Location) => {
    dispatch(actions.setMetadata({ location: next }));
  };

  const handleVisibilityChange = (next: Visibility) => {
    dispatch(actions.setMetadata({ visibility: next }));
  };

  const handleSpaceChange = (next?: string) => {
    dispatch(actions.setMetadata({ space: next, ...(!next && visibility === Visibility.SPACE ? { visibility: Visibility.PRIVATE } : {}) }));
  };

  const commitButton = justSaved ? (
    <Button size="sm" className="h-9 px-4 pointer-coarse:h-10" disabled>
      {t("editor.saved")}
      <CheckIcon className="size-3.5" strokeWidth={2.5} />
    </Button>
  ) : (
    <Button
      size="sm"
      className="h-9 min-w-16 rounded-lg px-4 shadow-none pointer-coarse:h-10"
      title={`${primaryModifierGlyph()} ↵ 保存`}
      onClick={onSave}
      disabled={isSaving || !valid}
    >
      {commitLabel}
      {isSaving && <LoaderIcon className="size-3.5 animate-spin" />}
    </Button>
  );

  return (
    // Writing stays primary; optional tools remain quiet and wrap only when needed.
    <div className="flex w-full min-w-0 flex-row items-end justify-between gap-2">
      <div className="flex min-w-0 flex-1 flex-row flex-wrap items-center justify-start gap-0.5">
        <InsertMenu
          isUploading={isUploading}
          isSaving={committing}
          location={location}
          onLocationChange={handleLocationChange}
          memoName={memoName}
          onAudioRecorderClick={onAudioRecorderClick}
          viewToggles={viewToggles}
          onInsertImages={onInsertImages}
          onInsertTag={onInsertTag}
        />
        {!parentMemoName && (
          <PartitionPicker
            value={partitionId}
            suspended={suspended}
            onChange={(value) =>
              dispatch(
                actions.setMetadata({
                  journalPartitionId: value,
                  journalPartitionSuspended: false,
                  journalPartitionExplicit: true,
                }),
              )
            }
            onSyncChange={syncChange}
            disabled={committing}
          />
        )}
        {Boolean(space) && (
          <AudienceMenu
            value={visibility}
            space={space}
            onChange={handleVisibilityChange}
            onSpaceChange={canChooseSpace ? handleSpaceChange : undefined}
            disabled={committing}
          />
        )}
      </div>

      <div className="flex shrink-0 flex-row items-center justify-end gap-1">
        {onCancel && (
          <Button variant="quiet" size="sm" onClick={onCancel} disabled={committing}>
            {t("common.cancel")}
          </Button>
        )}

        {blockedMessage ? (
          <Tooltip>
            <TooltipTrigger render={<span className="inline-flex" tabIndex={0} aria-label={blockedMessage} />}>
              {commitButton}
            </TooltipTrigger>
            <TooltipContent side="top">{blockedMessage}</TooltipContent>
          </Tooltip>
        ) : (
          commitButton
        )}
      </div>
    </div>
  );
};

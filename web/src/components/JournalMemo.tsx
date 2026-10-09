import { timestampDate } from "@bufbuild/protobuf/wkt";
import { MoreHorizontalIcon } from "lucide-react";
import { type MouseEvent, useMemo, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { CLAMP_PREVIEW_HEIGHT_PX } from "@/components/ClampedSection";
import JournalRecordActions from "@/components/JournalRecordActions";
import JournalReviewActions from "@/components/JournalReviewActions";
import MemoContent from "@/components/MemoContent";
import { AttachmentGallery, MemoMetadataRows } from "@/components/MemoMetadata";
import { separateAttachments } from "@/components/MemoMetadata/Attachment/attachmentHelpers";
import { useImagePreview } from "@/components/MemoView/hooks/useImagePreview";
import { createMemoNavigationState } from "@/components/MemoView/navigation";
import PreviewImageDialog from "@/components/PreviewImageDialog";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useAuth } from "@/contexts/AuthContext";
import { useJournalSession } from "@/contexts/JournalSessionContext";
import { useJournalPreferences } from "@/hooks/useJournalQueries";
import { isMemoBlurred } from "@/lib/tag";
import type { Memo } from "@/types/proto/api/v1/memo_service_pb";
import { filterInlineManagedAttachments } from "@/utils/managed-attachment";

/** An original record, with no social counters, automatic playback or generated preamble. */
export default function JournalMemo({ memo }: { memo: Memo }) {
  const location = useLocation();
  const navigate = useNavigate();
  const parentPage = `${location.pathname}${location.search}`;
  const preferences = useJournalPreferences();
  const { expanded, setExpanded } = useJournalSession();
  const { userTagsSetting } = useAuth();
  const [revealed, setRevealed] = useState(false);
  const hidden = isMemoBlurred(memo, userTagsSetting) && !revealed;
  const isExpanded = expanded.has(memo.name);
  const long = memo.content.length > 700;
  const { previewState, openPreview, setPreviewOpen } = useImagePreview();
  const handleMemoContentClick = (event: MouseEvent) => {
    const target = event.target;
    if (!(target instanceof HTMLImageElement) || target.closest("a")) return;
    const source = target.getAttribute("src");
    if (source) openPreview(source);
  };
  const attachments = useMemo(() => separateAttachments(filterInlineManagedAttachments(memo.content, memo.attachments)), [memo]);
  const date = memo.createTime ? timestampDate(memo.createTime) : undefined;
  const dateText = date?.toLocaleString("zh-CN", {
    timeZone: preferences.data?.timezone || "Asia/Shanghai",
    dateStyle: "long",
    timeStyle: "short",
  });

  return (
    <article className="rounded-xl border border-border/60 bg-card px-5 py-5 sm:px-7 sm:py-6">
      <header className="mb-4 flex min-h-11 items-center justify-between gap-3">
        <Link
          to={`/${memo.name}`}
          state={createMemoNavigationState(parentPage)}
          className="rounded-md text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-4"
        >
          <time dateTime={date?.toISOString()}>{dateText || "打开原记录"}</time>
        </Link>
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="icon" className="min-h-11 min-w-11" aria-label="记录选项" />}>
            <MoreHorizontalIcon className="size-4" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <JournalRecordActions name={memo.name} />
            <DropdownMenuItem
              onClick={() => navigate(`/${memo.name}`, { state: createMemoNavigationState(parentPage) })}
              className="min-h-11"
            >
              打开原记录
            </DropdownMenuItem>
            <JournalReviewActions name={memo.name} />
          </DropdownMenuContent>
        </DropdownMenu>
      </header>
      {hidden ? (
        <Button variant="outline" onClick={() => setRevealed(true)}>
          显示这条记录
        </Button>
      ) : (
        <div className="space-y-4">
          <div
            className={long && !isExpanded ? "relative overflow-hidden" : undefined}
            style={long && !isExpanded ? { maxHeight: CLAMP_PREVIEW_HEIGHT_PX } : undefined}
          >
            <MemoContent
              memoName={memo.name}
              parentPage={parentPage}
              content={memo.content}
              attachments={memo.attachments}
              contentClassName="leading-7"
              compact={long && !isExpanded}
              readonly
              onClick={handleMemoContentClick}
            />
            {long && !isExpanded && (
              <div className="pointer-events-none absolute inset-x-0 bottom-0 h-12 bg-linear-to-t from-card to-transparent" />
            )}
          </div>
          {long && (
            <Button
              variant="ghost"
              onClick={() => setExpanded(memo.name, !isExpanded)}
              aria-expanded={isExpanded}
              className="min-h-11 px-0 text-muted-foreground"
            >
              {isExpanded ? "收起" : "展开全文"}
            </Button>
          )}
          <AttachmentGallery visual={attachments.visual} onImagePreview={openPreview} />
          <MemoMetadataRows
            audio={attachments.audio}
            docs={attachments.docs}
            relations={[]}
            currentMemoName={memo.name}
            parentPage={parentPage}
          />
        </div>
      )}
      {previewState.items.length > 0 && (
        <PreviewImageDialog
          open={previewState.open}
          onOpenChange={setPreviewOpen}
          items={previewState.items}
          initialIndex={previewState.index}
        />
      )}
    </article>
  );
}

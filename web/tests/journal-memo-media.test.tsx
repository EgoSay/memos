import { create } from "@bufbuild/protobuf";
import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { CLAMP_PREVIEW_HEIGHT_PX } from "@/components/ClampedSection";
import JournalMemo from "@/components/JournalMemo";
import { MemoFilterProvider } from "@/contexts/MemoFilterContext";
import { AttachmentSchema } from "@/types/proto/api/v1/attachment_service_pb";
import { type Memo, MemoSchema } from "@/types/proto/api/v1/memo_service_pb";
import { getAttachmentUrl } from "@/utils/attachment";
import type { PreviewMediaItem } from "@/utils/media-item";

vi.mock("@/contexts/AuthContext", () => ({ useAuth: () => ({ userTagsSetting: undefined }) }));
vi.mock("@/hooks/useJournalQueries", () => ({ useJournalPreferences: () => ({ data: { timezone: "UTC" } }) }));
vi.mock("@/utils/i18n", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/utils/i18n")>()),
  useTranslate: () => (key: string) => key,
}));
vi.mock("@/components/MemoContent/MentionResolutionContext", () => ({ useResolvedMentionUsernames: () => new Set<string>() }));
vi.mock("@/components/JournalRecordActions", () => ({ default: () => null }));
vi.mock("@/components/JournalReviewActions", () => ({ default: () => null }));
vi.mock("@/contexts/JournalSessionContext", () => ({
  useJournalSession: () => {
    const [expanded, update] = useState<ReadonlySet<string>>(new Set());
    return {
      expanded,
      setExpanded: (name: string, open: boolean) => update(open ? new Set([name]) : new Set()),
    };
  },
}));
vi.mock("@/components/PreviewImageDialog", () => ({
  default: ({ open, items }: { open: boolean; items: PreviewMediaItem[] }) =>
    open ? (
      <div role="dialog" aria-label="照片预览">
        {items[0]?.kind === "image" ? items[0].sourceUrl : ""}
      </div>
    ) : null,
}));

const photo = create(AttachmentSchema, { name: "attachments/photo-one", filename: "weekend.jpg", type: "image/jpeg" });
const record = (content: string, attachments = [photo]) => create(MemoSchema, { name: "memos/one", content, attachments });
const show = (memo: Memo) =>
  render(
    <MemoryRouter initialEntries={["/journal/day/2025-10-03"]}>
      <MemoFilterProvider>
        <JournalMemo memo={memo} />
      </MemoFilterProvider>
    </MemoryRouter>,
  );

describe("journal original Markdown and media", () => {
  it("renders real tags and task status without a MemoViewProvider or a mutable task handler", () => {
    show(record("散步回来 #日常\n\n- [x] 看过夕阳\n- [ ] 还没整理照片"));
    expect(screen.getByText("#日常")).toHaveAttribute("data-tag", "日常");
    const tasks = screen.getAllByRole("checkbox");
    expect(tasks).toHaveLength(2);
    expect(tasks[0]).toHaveAttribute("aria-checked", "true");
    expect(tasks[1]).toHaveAttribute("aria-checked", "false");
    for (const task of tasks) expect(task).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(tasks[1]);
    expect(tasks[1]).toHaveAttribute("aria-checked", "false");
    expect(screen.getByRole("img", { name: photo.filename })).toBeInTheDocument();
  });
  it("keeps Markdown and inline photos rendered while a long original is collapsed", () => {
    const memo = record(`# 周末\n\n**原来的重点**\n\n![窗边](/file/attachments/photo-one)\n\n${"平凡的一天。".repeat(150)}`);
    show(memo);
    expect(screen.getByRole("heading", { name: "周末" })).toBeInTheDocument();
    expect(screen.getByText("原来的重点").tagName).toBe("STRONG");
    const image = screen.getByRole("img", { name: "窗边" });
    expect(image).toHaveAttribute("src", getAttachmentUrl(photo));
    expect(screen.getAllByRole("img")).toHaveLength(1);
    const clamp = image.closest("[data-memo-content]")?.parentElement?.parentElement;
    expect(clamp).toHaveStyle({ maxHeight: `${CLAMP_PREVIEW_HEIGHT_PX}px` });
    fireEvent.click(screen.getByRole("button", { name: "展开全文" }));
    expect(screen.getByRole("button", { name: "收起" })).toHaveAttribute("aria-expanded", "true");
    expect(clamp?.getAttribute("style")).not.toContain("max-height");
    expect(screen.getByText("原来的重点").tagName).toBe("STRONG");
  });

  it("opens the managed inline image in the existing preview with its resolved private URL", () => {
    show(record("![窗边](/file/attachments/photo-one)"));
    fireEvent.mouseUp(screen.getByRole("img", { name: "窗边" }));
    expect(screen.getByRole("dialog", { name: "照片预览" })).toHaveTextContent(getAttachmentUrl(photo));
  });

  it("keeps a deliberately linked image as a link without opening a second preview", () => {
    show(record("[![窗边](/file/attachments/photo-one)](https://example.com/photo)"));
    fireEvent.mouseUp(screen.getByRole("img", { name: "窗边" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "窗边" })).toHaveAttribute("href", "https://example.com/photo");
  });

  it("renders photo and audio attachments without any text and does not preload audio", () => {
    const audio = create(AttachmentSchema, { name: "attachments/voice-one", filename: "散步.m4a", type: "audio/mp4" });
    const { container } = show(record("", [photo, audio]));
    expect(screen.getByRole("img", { name: photo.filename })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Play 散步.m4a" })).toBeInTheDocument();
    expect(container.querySelector("audio")).toHaveAttribute("preload", "none");
    expect(container.querySelector("audio")).not.toHaveAttribute("src");
  });
});

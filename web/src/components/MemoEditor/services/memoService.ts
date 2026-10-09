import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema, timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { isEqual } from "lodash-es";
import { getEditorReferenceRelations } from "@/components/MemoMetadata/Relation/relationHelpers";
import { memoServiceClient } from "@/connect";
import { finishPartitionSave } from "@/hooks/useJournalPartitionQueries";
import { journalRecordVersion } from "@/lib/journal-record-version";
import type { Attachment } from "@/types/proto/api/v1/attachment_service_pb";
import { AttachmentSchema } from "@/types/proto/api/v1/attachment_service_pb";
import type { Memo } from "@/types/proto/api/v1/memo_service_pb";
import { MemoSchema } from "@/types/proto/api/v1/memo_service_pb";
import type { EditorState } from "../state";
import { uploadService } from "./uploadService";

/**
 * Converts attachments to reference format for API requests.
 * The backend only needs the attachment name to link it to a memo.
 */
function toAttachmentReferences(attachments: Attachment[]): Attachment[] {
  return attachments.map((a) => create(AttachmentSchema, { name: a.name }));
}

function buildUpdateMask(
  prevMemo: Memo,
  state: EditorState,
  allAttachments: typeof state.metadata.attachments,
): { mask: Set<string>; patch: Partial<Memo> } {
  const mask = new Set<string>();
  const patch: Partial<Memo> = {
    name: prevMemo.name,
    content: state.content,
  };

  if (!isEqual(state.content, prevMemo.content)) {
    mask.add("content");
    patch.content = state.content;
  }
  if (!isEqual(state.metadata.visibility, prevMemo.visibility)) {
    mask.add("visibility");
    patch.visibility = state.metadata.visibility;
  }
  // Placement and audience are validated together: a move always states the
  // audience it lands with, so a Space-only audience never follows it implicitly.
  if ((state.metadata.space || undefined) !== (prevMemo.space || undefined)) {
    mask.add("space");
    mask.add("visibility");
    patch.space = state.metadata.space ?? "";
    patch.visibility = state.metadata.visibility;
  }
  if (!isEqual(allAttachments, prevMemo.attachments)) {
    mask.add("attachments");
    patch.attachments = toAttachmentReferences(allAttachments);
  }
  const previousReferenceRelations = getEditorReferenceRelations(prevMemo.relations, prevMemo.name);
  const nextReferenceRelations = getEditorReferenceRelations(state.metadata.relations, prevMemo.name);
  if (!isEqual(nextReferenceRelations, previousReferenceRelations)) {
    mask.add("relations");
    patch.relations = nextReferenceRelations;
  }
  if (!isEqual(state.metadata.location, prevMemo.location)) {
    mask.add("location");
    patch.location = state.metadata.location;
  }

  // Auto-update timestamp if content changed
  if (["content", "attachments", "relations", "location"].some((key) => mask.has(key))) {
    mask.add("update_time");
  }

  // Handle custom timestamps
  if (state.timestamps.createTime) {
    const prevCreateTime = prevMemo.createTime ? timestampDate(prevMemo.createTime) : undefined;
    if (!isEqual(state.timestamps.createTime, prevCreateTime)) {
      mask.add("create_time");
      patch.createTime = timestampFromDate(state.timestamps.createTime);
    }
  }
  if (state.timestamps.updateTime) {
    const prevUpdateTime = prevMemo.updateTime ? timestampDate(prevMemo.updateTime) : undefined;
    if (!isEqual(state.timestamps.updateTime, prevUpdateTime)) {
      mask.add("update_time");
      patch.updateTime = timestampFromDate(state.timestamps.updateTime);
    }
  }

  return { mask, patch };
}

async function finishJournalSave(state: EditorState, memoName: string): Promise<string | undefined> {
  if (state.metadata.journalPartitionId === undefined || state.metadata.journalPartitionSuspended) return undefined;
  try {
    await finishPartitionSave(memoName, state.metadata.journalPartitionId, state.metadata.journalPartitionExplicit ?? false);
  } catch (error) {
    return `正文和附件已保存，分区同步尚未完成：${error instanceof Error ? error.message : "请到分区设置重试"}`;
  }
  return undefined;
}

export const memoService = {
  async save(
    state: EditorState,
    options: {
      memoName?: string;
      parentMemoName?: string;
      space?: string;
    },
  ): Promise<{ memoName: string; hasChanges: boolean; moved?: boolean; syncError?: string }> {
    // 1. Upload local files first
    const newAttachments = await uploadService.uploadFiles(state.localFiles);
    const allAttachments = [...state.metadata.attachments, ...newAttachments];

    // 2. Update existing memo
    if (options.memoName) {
      const prevMemo = await memoServiceClient.getMemo({ name: options.memoName });
      const baseline = state.baselineMemo ?? prevMemo;
      if (
        state.baselineMemo &&
        (!isEqual(prevMemo.content, baseline.content) ||
          !isEqual(prevMemo.attachments, baseline.attachments) ||
          !isEqual(prevMemo.location, baseline.location) ||
          !isEqual(prevMemo.createTime, baseline.createTime) ||
          prevMemo.visibility !== baseline.visibility ||
          prevMemo.space !== baseline.space)
      ) {
        throw new ConnectError(
          "这条记录已在其他页面或设备修改。当前输入已保留，请复制后重新打开记录，查看双方内容再决定如何合并。",
          Code.Aborted,
        );
      }
      const { mask, patch } = buildUpdateMask(baseline, state, allAttachments);

      if (mask.size === 0) {
        return { memoName: prevMemo.name, hasChanges: false };
      }

      const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(baseline.content));
      const expectedContent = Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
      const memo = await memoServiceClient.updateMemo(
        {
          memo: create(MemoSchema, patch as Record<string, unknown>),
          updateMask: create(FieldMaskSchema, { paths: Array.from(mask) }),
        },
        {
          headers: {
            "X-Memos-Expected-Content-Sha256": expectedContent,
            "X-Memos-Expected-Record-Sha256": await journalRecordVersion(baseline),
          },
        },
      );
      return { memoName: memo.name, hasChanges: true, moved: mask.has("space"), syncError: await finishJournalSave(state, memo.name) };
    }

    // 3. Create new memo or comment
    const memoData = create(MemoSchema, {
      content: state.content,
      visibility: state.metadata.visibility,
      attachments: toAttachmentReferences(allAttachments),
      relations: state.metadata.relations,
      location: state.metadata.location,
      createTime: state.timestamps.createTime ? timestampFromDate(state.timestamps.createTime) : undefined,
      updateTime: state.timestamps.updateTime ? timestampFromDate(state.timestamps.updateTime) : undefined,
      space: options.parentMemoName ? undefined : (options.space ?? state.metadata.space),
    });

    let memo: Memo;
    try {
      memo = options.parentMemoName
        ? await memoServiceClient.createMemoComment({
            name: options.parentMemoName,
            comment: memoData,
          })
        : await memoServiceClient.createMemo({ memo: memoData, memoId: state.clientId });
    } catch (error) {
      if (options.parentMemoName || !state.clientId || ConnectError.from(error).code !== Code.AlreadyExists) throw error;
      // A lost success response is resolved by the stable client ID. Never
      // claim a mismatched record as this save's result.
      const existing = await memoServiceClient.getMemo({ name: `memos/${state.clientId}` });
      if (
        existing.content !== memoData.content ||
        existing.visibility !== memoData.visibility ||
        !isEqual(existing.attachments.map((item) => item.name).sort(), allAttachments.map((item) => item.name).sort())
      )
        throw error;
      memo = existing;
    }

    return { memoName: memo.name, hasChanges: true, syncError: await finishJournalSave(state, memo.name) };
  },

  /**
   * Build the INIT_MEMO payload from an already-loaded Memo entity (no network
   * request). Returns only the fields the reducer's INIT_MEMO case consumes —
   * UI state (mode, loading flags, …) is owned by the reducer, not by memos.
   */
  fromMemo(memo: Memo): Pick<EditorState, "content" | "metadata" | "timestamps" | "baselineMemo"> {
    return {
      baselineMemo: memo,
      content: memo.content,
      metadata: {
        visibility: memo.visibility,
        space: memo.space || undefined,
        attachments: memo.attachments,
        relations: memo.relations,
        location: memo.location,
      },
      timestamps: {
        createTime: memo.createTime ? timestampDate(memo.createTime) : undefined,
        updateTime: memo.updateTime ? timestampDate(memo.updateTime) : undefined,
      },
    };
  },
};

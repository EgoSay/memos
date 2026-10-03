import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { memoService } from "@/components/MemoEditor/services/memoService";
import { createInitialState } from "@/components/MemoEditor/state";
import { restoreJournalState, snapshotJournalState } from "@/lib/journal-drafts";
import { MemoSchema, Visibility } from "@/types/proto/api/v1/memo_service_pb";

const client = vi.hoisted(() => ({ getMemo: vi.fn(), updateMemo: vi.fn(), createMemo: vi.fn() }));
vi.mock("@/connect", () => ({ memoServiceClient: client, attachmentServiceClient: {} }));

describe("journal save reliability", () => {
  beforeEach(() => vi.clearAllMocks());
  it("resolves a lost creation response without creating another record", async () => {
    const state = { ...createInitialState(), content: "ordinary day" };
    const existing = create(MemoSchema, { name: `memos/${state.clientId}`, content: state.content, visibility: Visibility.PRIVATE });
    client.createMemo.mockRejectedValue(new ConnectError("exists", Code.AlreadyExists));
    client.getMemo.mockResolvedValue(existing);
    const result = await memoService.save(state, {});
    expect(result.memoName).toBe(existing.name);
    expect(client.createMemo.mock.calls[0][0].memoId).toBe(state.clientId);
    expect(client.updateMemo).not.toHaveBeenCalled();
  });
  it("does not mistake a mismatched identifier for a successful retry", async () => {
    const state = { ...createInitialState(), content: "mine" };
    client.createMemo.mockRejectedValue(new ConnectError("exists", Code.AlreadyExists));
    client.getMemo.mockResolvedValue(create(MemoSchema, { content: "another version", visibility: Visibility.PRIVATE }));
    await expect(memoService.save(state, {})).rejects.toMatchObject({ code: Code.AlreadyExists });
  });
  it("preserves a local edit when another device changed the original", async () => {
    const baseline = create(MemoSchema, { name: "memos/one", content: "original", visibility: Visibility.PRIVATE });
    const state = { ...createInitialState(), ...memoService.fromMemo(baseline), content: "my local edit" };
    client.getMemo.mockResolvedValue(create(MemoSchema, { ...baseline, content: "other device" }));
    await expect(memoService.save(state, { memoName: baseline.name })).rejects.toMatchObject({ code: Code.Aborted });
    expect(state.content).toBe("my local edit");
    expect(state.baselineMemo?.content).toBe("original");
    expect(client.updateMemo).not.toHaveBeenCalled();
  });
  it("captures raw media and stable IDs independently of temporary preview URLs", async () => {
    const file = new File([new Uint8Array([1, 2, 3])], "voice.webm", { type: "audio/webm" });
    const state = { ...createInitialState(), content: "", localFiles: [{ file, previewUrl: "blob:old", clientId: "stable-file" }] };
    const snapshot = await snapshotJournalState(state);
    expect(snapshot.localFiles[0].file).toBe(file);
    expect(snapshot.localFiles[0]).not.toHaveProperty("previewUrl");
    vi.stubGlobal("URL", { createObjectURL: () => "blob:new" });
    const restored = restoreJournalState({ key: "key", owner: "user", kind: "draft", state: snapshot, updatedAt: 1 });
    expect(restored.localFiles[0].previewUrl).toBe("blob:new");
    expect(restored.localFiles[0].clientId).toBe("stable-file");
    expect(restored.clientId).toBe(state.clientId);
    expect(restored.ui.isLoading.saving).toBe(false);
    vi.unstubAllGlobals();
  });
});

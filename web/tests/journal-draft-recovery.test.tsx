import { create } from "@bufbuild/protobuf";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PendingRecords } from "@/components/MemoEditor/components/PendingRecords";
import { RecordConflict } from "@/components/MemoEditor/components/RecordConflict";
import { useDurableDraft } from "@/components/MemoEditor/hooks/useDurableDraft";
import { createInitialState, EditorProvider, type EditorState, useEditorContext } from "@/components/MemoEditor/state";
import { type JournalDraft, journalDrafts } from "@/lib/journal-drafts";
import { MemoSchema } from "@/types/proto/api/v1/memo_service_pb";

const service = vi.hoisted(() => ({ save: vi.fn(), getMemo: vi.fn() }));
vi.mock("@/components/MemoEditor/services/memoService", () => ({ memoService: service }));
vi.mock("@/connect", () => ({ memoServiceClient: { getMemo: service.getMemo } }));
vi.mock("@/components/MemoEditor/loader", () => ({ loadMemoEditor: async () => ({ default: () => <output>resumed editor</output> }) }));
let editor: ReturnType<typeof useEditorContext>;
function Probe({ durable = false }: { durable?: boolean }) {
  editor = useEditorContext();
  const { status } = useDurableDraft("users/alice", "home", durable);
  return <output data-testid="durable-status">{status}</output>;
}
function draft(key: string, kind: JournalDraft["kind"], content: string): JournalDraft {
  return { key, kind, owner: "users/alice", updatedAt: 1, state: { ...createInitialState(), content, localFiles: [] } };
}
const mount = (node: React.ReactNode, state: EditorState = createInitialState(), durable = false) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <EditorProvider initialEditorState={state}>
        <Probe durable={durable} />
        {node}
      </EditorProvider>
    </QueryClientProvider>,
  );

beforeEach(() => {
  service.save.mockReset().mockResolvedValue({ memoName: "memos/new", hasChanges: true });
  service.getMemo.mockReset();
  vi.spyOn(journalDrafts, "get").mockResolvedValue(undefined);
  vi.spyOn(journalDrafts, "list").mockResolvedValue([]);
  vi.spyOn(journalDrafts, "pending").mockResolvedValue([]);
  vi.spyOn(journalDrafts, "save").mockImplementation(async (key, owner, state, options, kind = "draft") => ({
    key,
    owner,
    state: { ...state, localFiles: [] },
    options,
    kind,
    updatedAt: 1,
  }));
  vi.spyOn(journalDrafts, "remove").mockResolvedValue();
});
afterEach(() => vi.restoreAllMocks());

describe("durable draft recovery", () => {
  it("retains the edit target and removes a deliberately cleared draft", async () => {
    const baseline = create(MemoSchema, { name: "memos/existing", content: "old" });
    mount(null, { ...createInitialState(), content: "local revision", baselineMemo: baseline }, true);
    await waitFor(() => expect(screen.getByTestId("durable-status")).toHaveTextContent("saved"));
    expect(journalDrafts.save).toHaveBeenCalledWith("home", "users/alice", expect.anything(), { memoName: baseline.name });
    await act(async () => editor.dispatch(editor.actions.updateContent("")));
    await waitFor(() => expect(screen.getByTestId("durable-status")).toHaveTextContent("idle"));
    expect(journalDrafts.remove).toHaveBeenCalledWith("home");
  });
  it("does not claim a local save succeeded when storage is full", async () => {
    vi.mocked(journalDrafts.save).mockRejectedValue(new DOMException("Full", "QuotaExceededError"));
    mount(null, { ...createInitialState(), content: "keep me" }, true);
    await waitFor(() => expect(screen.getByTestId("durable-status")).toHaveTextContent("failed"));
    expect(editor.getState().content).toBe("keep me");
  });
  it("never replaces an occupied editor with an offline draft", async () => {
    vi.mocked(journalDrafts.list).mockResolvedValue([draft("offline:users/alice", "draft", "from offline")]);
    mount(<PendingRecords owner="users/alice" draftKey="home" />, { ...createInitialState(), content: "already writing" });
    fireEvent.click(await screen.findByText(/离线草稿 · from offline/));
    fireEvent.click(screen.getByRole("button", { name: "继续写这份草稿" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("请先保存或清空");
    expect(editor.getState().content).toBe("already writing");
    expect(journalDrafts.remove).not.toHaveBeenCalled();
  });
  it("moves an offline draft only after committing the new draft location", async () => {
    const offline = draft("offline:users/alice", "draft", "from offline");
    vi.mocked(journalDrafts.list).mockResolvedValue([offline]);
    mount(<PendingRecords owner="users/alice" draftKey="home" />);
    fireEvent.click(await screen.findByText(/离线草稿 · from offline/));
    fireEvent.click(screen.getByRole("button", { name: "继续写这份草稿" }));
    await waitFor(() => expect(editor.getState().content).toBe("from offline"));
    expect(editor.getState().clientId).toBe(offline.state.clientId);
    expect(editor.getState().contentSource).toBe("external");
    expect(journalDrafts.save).toHaveBeenCalledWith("home", "users/alice", expect.anything(), undefined);
    expect(journalDrafts.remove).toHaveBeenCalledWith(offline.key);
    expect(vi.mocked(journalDrafts.save).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(journalDrafts.remove).mock.invocationCallOrder[0],
    );
  });
  it("keeps a conflicting pending edit while allowing another pending record to sync", async () => {
    const first = draft("pending:one", "pending", "conflicting edit");
    const second = draft("pending:two", "pending", "another day");
    vi.mocked(journalDrafts.pending).mockResolvedValue([first, second]);
    vi.mocked(journalDrafts.list).mockResolvedValue([first, second]);
    service.save.mockRejectedValueOnce(new Error("other device changed it"));
    mount(<PendingRecords owner="users/alice" draftKey="home" />);
    await waitFor(() => expect(journalDrafts.remove).toHaveBeenCalledWith(second.key));
    expect(journalDrafts.remove).not.toHaveBeenCalledWith(first.key);
    expect(await screen.findByRole("alert")).toHaveTextContent("other device changed it");
  });
  it("moves a conflicting pending edit back into its own editor without losing the original baseline", async () => {
    const entry = draft("pending:edit", "pending", "my pending edit");
    const baseline = create(MemoSchema, { name: "memos/existing", content: "original" });
    entry.options = { memoName: baseline.name };
    entry.state.baselineMemo = baseline;
    vi.mocked(journalDrafts.pending).mockResolvedValue([entry]);
    vi.mocked(journalDrafts.list).mockResolvedValue([entry]);
    service.save.mockRejectedValue(new Error("conflicting versions"));
    service.getMemo.mockResolvedValue(create(MemoSchema, { name: baseline.name, content: "remote edit" }));
    mount(<PendingRecords owner="users/alice" draftKey="home" />);
    await screen.findByRole("alert");
    fireEvent.click(screen.getByText(/等待同步 · my pending edit/));
    const resume = screen.getByRole("button", { name: "继续处理这份修改" });
    await waitFor(() => expect(resume).not.toBeDisabled());
    fireEvent.click(resume);
    expect(await screen.findByText("resumed editor")).toBeInTheDocument();
    expect(journalDrafts.save).toHaveBeenCalledWith(
      expect.stringContaining("edit:memos/existing"),
      "users/alice",
      expect.objectContaining({ content: "my pending edit", baselineMemo: baseline }),
      entry.options,
    );
    expect(journalDrafts.remove).toHaveBeenCalledWith(entry.key);
    expect(vi.mocked(journalDrafts.save).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(journalDrafts.remove).mock.invocationCallOrder[0],
    );
  });
  it("keeps both contents available until the owner explicitly accepts a new comparison baseline", () => {
    const previous = create(MemoSchema, { name: "memos/one", content: "original" });
    const latest = create(MemoSchema, { name: "memos/one", content: "other device" });
    const close = vi.fn();
    mount(<RecordConflict latest={latest} onClose={close} />, {
      ...createInitialState(),
      baselineMemo: previous,
      content: "my local edit",
    });
    expect(screen.getByText("my local edit")).toBeInTheDocument();
    expect(screen.getByText("other device")).toBeInTheDocument();
    expect(editor.getState().baselineMemo).toBe(previous);
    fireEvent.click(screen.getByRole("button", { name: "已比较，继续编辑" }));
    expect(editor.getState().content).toBe("my local edit");
    expect(editor.getState().baselineMemo).toBe(latest);
    expect(service.save).not.toHaveBeenCalled();
    expect(close).toHaveBeenCalledOnce();
  });
});

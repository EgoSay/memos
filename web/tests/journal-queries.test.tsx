import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { journalRequest, useJournalCandidates } from "@/hooks/useJournalQueries";
import { State } from "@/types/proto/api/v1/common_pb";
import { MemoSchema } from "@/types/proto/api/v1/memo_service_pb";

const { listMemos, getRequestToken } = vi.hoisted(() => ({ listMemos: vi.fn(), getRequestToken: vi.fn() }));
vi.mock("@/connect", () => ({ memoServiceClient: { listMemos }, getRequestToken }));
vi.mock("@/hooks/useCurrentUser", () => ({ default: () => ({ name: "users/me" }) }));
vi.mock("@/hooks/useMemoQueries", () => ({ memoKeys: { lists: () => ["memos", "list"] }, memoDetailQueryOptions: vi.fn() }));

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      {children}
    </QueryClientProvider>
  );
}
const memo = (name: string) =>
  create(MemoSchema, {
    name: `memos/${name}`,
    creator: "users/me",
    state: State.NORMAL,
    createTime: timestampFromDate(new Date("2020-01-01T12:00:00Z")),
  });

beforeEach(() => {
  listMemos.mockReset();
  getRequestToken.mockResolvedValue("test-token");
});
afterEach(() => vi.unstubAllGlobals());

describe("journal data access", () => {
  it("loads every page before presenting a random pool and excludes comments and other users", async () => {
    listMemos
      .mockResolvedValueOnce({ memos: [memo("a")], nextPageToken: "second" })
      .mockResolvedValueOnce({
        memos: [memo("b"), { ...memo("other"), creator: "users/other" }, { ...memo("comment"), parent: "memos/a" }],
        nextPageToken: "third",
      })
      .mockResolvedValueOnce({ memos: [memo("c")], nextPageToken: "" });
    const { result } = renderHook(() => useJournalCandidates("2026-10-03", "Asia/Shanghai", ["memos/b"]), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.map((item) => item.name)).toEqual(["memos/a", "memos/c"]);
    expect(listMemos).toHaveBeenCalledTimes(3);
    expect(listMemos.mock.calls[1][0]).toMatchObject({ pageToken: "second", filter: 'creator == "users/me"' });
  });
  it("does not treat a partial archive as a complete random sample when a later page fails", async () => {
    listMemos
      .mockResolvedValueOnce({ memos: [memo("a")], nextPageToken: "second" })
      .mockRejectedValueOnce(new Error("network unavailable"));
    const { result } = renderHook(() => useJournalCandidates("2026-10-03", "Asia/Shanghai", []), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });
  it("stops a malformed pagination loop", async () => {
    listMemos.mockResolvedValue({ memos: [memo("a")], nextPageToken: "same" });
    const { result } = renderHook(() => useJournalCandidates("2026-10-03", "Asia/Shanghai", []), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(listMemos).toHaveBeenCalledTimes(2);
  });
  it("uses refreshed session authorization and supports body-free successful deletions", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    await expect(journalRequest("/partitions/one", { method: "DELETE" })).resolves.toBeUndefined();
    expect(getRequestToken).toHaveBeenCalledOnce();
    const [url, options] = fetch.mock.calls[0];
    expect(url).toBe("/api/v1/journal/partitions/one");
    expect(options.headers.get("Authorization")).toBe("Bearer test-token");
    expect(options.credentials).toBe("same-origin");
  });
});

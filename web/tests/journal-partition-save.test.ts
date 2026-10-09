import { beforeEach, describe, expect, it, vi } from "vitest";
import { finishPartitionSave } from "@/hooks/useJournalPartitionQueries";

const request = vi.hoisted(() => vi.fn());
vi.mock("@/hooks/useJournalQueries", () => ({ journalRequest: request }));
vi.mock("@/hooks/useCurrentUser", () => ({ default: () => undefined }));

beforeEach(() => request.mockReset());
describe("complete saved record partition authorization", () => {
  it("never reactivates a restored record during an ordinary edit", async () => {
    request.mockResolvedValue({ memoUid: "one", partitionId: "private", version: 4, suspended: true });
    await finishPartitionSave("memos/one", "private");
    expect(request).toHaveBeenCalledTimes(1);
  });
  it("allows an explicit new selection to reactivate the partition", async () => {
    request.mockResolvedValueOnce({ memoUid: "one", partitionId: "private", version: 4, suspended: true }).mockResolvedValue({});
    await finishPartitionSave("memos/one", "private", true);
    expect(request).toHaveBeenNthCalledWith(2, "/memos/one/partition", {
      method: "PUT",
      body: JSON.stringify({ partitionId: "private", version: 4 }),
    });
    expect(request).toHaveBeenNthCalledWith(3, "/memos/one/send", { method: "POST", body: JSON.stringify({ manual: false }) });
  });
  it("does not send an unpartitioned record", async () => {
    request.mockResolvedValue({ memoUid: "one", partitionId: "", version: 0 });
    await finishPartitionSave("memos/one", "");
    expect(request).toHaveBeenCalledTimes(1);
  });
  it("does not send after a conflicting assignment", async () => {
    request.mockResolvedValueOnce({ memoUid: "one", partitionId: "old", version: 2 }).mockRejectedValueOnce(new Error("conflict"));
    await expect(finishPartitionSave("one", "new", true)).rejects.toThrow("conflict");
    expect(request).toHaveBeenCalledTimes(2);
    expect(request.mock.calls.some(([path]) => path.endsWith("/send"))).toBe(false);
  });
});

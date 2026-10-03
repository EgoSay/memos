import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import JournalDay from "@/pages/JournalDay";
import { State } from "@/types/proto/api/v1/common_pb";

const { listMemos, preferences } = vi.hoisted(() => ({ listMemos: vi.fn(), preferences: { timezone: "Asia/Shanghai" } }));
vi.mock("@/connect", () => ({ memoServiceClient: { listMemos } }));
vi.mock("@/hooks/useCurrentUser", () => ({ default: () => ({ name: "users/me" }) }));
vi.mock("@/hooks/useJournalQueries", () => ({ useJournalPreferences: () => ({ data: preferences }) }));
vi.mock("@/components/JournalMemo", () => ({ default: () => null }));

beforeEach(() => {
  listMemos.mockResolvedValue({ memos: [], nextPageToken: "" });
});

function renderDay(date: string) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={[`/journal/day/${date}`]}>
        <Routes>
          <Route path="/journal/day/:date" element={<JournalDay />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("journal day ListMemos request contract", () => {
  // The corresponding Go API test executes this CEL shape against SQLite at
  // each boundary; this layer checks the actual routed page emits that shape.
  it.each([
    ["2025-10-03", "Asia/Shanghai", "2025-10-02T16:00:00Z", "2025-10-03T16:00:00Z"],
    ["2026-03-08", "America/New_York", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z"],
    ["2026-11-01", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"],
  ])("uses original record timestamps and real day boundaries for %s in %s", async (date, timezone, start, end) => {
    preferences.timezone = timezone;
    renderDay(date);
    await waitFor(() => expect(listMemos).toHaveBeenCalledTimes(2));
    const requests = listMemos.mock.calls.map(([request]) => request);
    expect(requests.map((request) => request.state).sort()).toEqual([State.NORMAL, State.ARCHIVED].sort());
    for (const request of requests) {
      expect(request.filter).toBe(
        `(creator == "users/me") && (created_ts >= timestamp(${Date.parse(start) / 1000}) && created_ts < timestamp(${Date.parse(end) / 1000}))`,
      );
      expect(request.orderBy).toBe("create_time desc");
    }
    expect(await screen.findByText("这一天没有记录。")).toBeInTheDocument();
  });

  it("rejects an invalid date without asking the API to execute a made-up range", () => {
    renderDay("2025-02-30");
    expect(screen.getByText("这个日期无效。")).toBeInTheDocument();
    expect(listMemos).not.toHaveBeenCalled();
  });
});

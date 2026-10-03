import { create } from "@bufbuild/protobuf";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import JournalReviewActions from "@/components/JournalReviewActions";
import JournalSourcePicker from "@/components/JournalSourcePicker";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { MemoSchema } from "@/types/proto/api/v1/memo_service_pb";

const { listMemos, mutate, preferences } = vi.hoisted(() => ({
  listMemos: vi.fn(),
  mutate: vi.fn(),
  preferences: { timezone: "America/New_York", excludedMemoNames: ["memos/other"] },
}));
vi.mock("@/connect", () => ({ memoServiceClient: { listMemos } }));
vi.mock("@/hooks/useCurrentUser", () => ({ default: () => ({ name: "users/me" }) }));
vi.mock("@/hooks/useJournalQueries", () => ({
  useJournalPreferences: () => ({ data: preferences }),
  useUpdateJournalPreferences: () => ({ mutate, isPending: false }),
}));

beforeEach(() => {
  vi.clearAllMocks();
  preferences.excludedMemoNames = ["memos/other"];
  listMemos.mockResolvedValue({
    memos: [create(MemoSchema, { name: "memos/one", creator: "users/me", content: "这一天的记录" })],
    nextPageToken: "",
  });
});

function renderPicker(onChange = vi.fn()) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <JournalSourcePicker selected={["memos/one"]} onChange={onChange} />
    </QueryClientProvider>,
  );
  return onChange;
}

describe("explicit source selection", () => {
  it("refreshes an open candidate list after a record is saved without changing its selection", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const onChange = vi.fn();
    listMemos.mockResolvedValueOnce({ memos: [], nextPageToken: "" });
    render(
      <QueryClientProvider client={client}>
        <JournalSourcePicker selected={[]} onChange={onChange} />
      </QueryClientProvider>,
    );
    await screen.findByText("没有找到记录。");
    await act(async () => {
      await client.invalidateQueries({ queryKey: ["memos", "list"] });
    });
    expect(await screen.findByText("这一天的记录")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("applies both ends of the selected local day across daylight saving without changing the selection", async () => {
    const onChange = renderPicker();
    await screen.findByText("这一天的记录");
    fireEvent.click(screen.getByText("按日期范围查找"));
    fireEvent.change(screen.getByLabelText("从哪一天"), { target: { value: "2026-03-08" } });
    fireEvent.change(screen.getByLabelText("到哪一天（含当天）"), { target: { value: "2026-03-08" } });
    expect(listMemos).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "查找" }));
    await waitFor(() => expect(listMemos).toHaveBeenCalledTimes(2));
    const filter = listMemos.mock.calls[1][0].filter;
    expect(filter).toContain(`created_ts >= timestamp(${Date.parse("2026-03-08T05:00:00Z") / 1000})`);
    expect(filter).toContain(`created_ts < timestamp(${Date.parse("2026-03-09T04:00:00Z") / 1000})`);
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("已选 1 条")).toBeInTheDocument();
  });

  it.each([
    ["从哪一天", "2026-03-08", ">=", "2026-03-08T05:00:00Z"],
    ["到哪一天（含当天）", "2026-03-08", "<", "2026-03-09T04:00:00Z"],
  ])("supports a range with only %s using the CEL timestamp contract", async (label, date, operator, instant) => {
    renderPicker();
    await screen.findByText("这一天的记录");
    fireEvent.click(screen.getByText("按日期范围查找"));
    fireEvent.change(screen.getByLabelText(label), { target: { value: date } });
    fireEvent.click(screen.getByRole("button", { name: "查找" }));
    await waitFor(() => expect(listMemos).toHaveBeenCalledTimes(2));
    expect(listMemos.mock.calls[1][0].filter).toBe(
      `(creator == "users/me") && (created_ts ${operator} timestamp(${Date.parse(instant) / 1000}))`,
    );
  });

  it("rejects a reversed date interval before issuing another request", async () => {
    renderPicker();
    await screen.findByText("这一天的记录");
    fireEvent.click(screen.getByText("按日期范围查找"));
    fireEvent.change(screen.getByLabelText("从哪一天"), { target: { value: "2026-10-04" } });
    fireEvent.change(screen.getByLabelText("到哪一天（含当天）"), { target: { value: "2026-10-03" } });
    fireEvent.click(screen.getByRole("button", { name: "查找" }));
    expect(screen.getByRole("alert")).toHaveTextContent("结束日期不能早于开始日期。");
    expect(listMemos).toHaveBeenCalledTimes(1);
  });
});

function LocationProbe() {
  const location = useLocation();
  return <output>{location.pathname + location.search}</output>;
}

async function renderActions() {
  render(
    <MemoryRouter>
      <DropdownMenu>
        <DropdownMenuTrigger>记录选项</DropdownMenuTrigger>
        <DropdownMenuContent>
          <JournalReviewActions name="memos/one" />
        </DropdownMenuContent>
      </DropdownMenu>
      <LocationProbe />
    </MemoryRouter>,
  );
  fireEvent.click(screen.getByRole("button", { name: "记录选项" }));
  await screen.findByRole("menuitem", { name: "用 AI 看看这条" });
}

describe("optional original record actions", () => {
  it("adds a review exclusion without removing other exclusions", async () => {
    await renderActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "暂不在回顾与漫步中出现" }));
    expect(mutate).toHaveBeenCalledWith({ excludedMemoNames: ["memos/other", "memos/one"] }, expect.any(Object));
  });

  it("restores only this record and opens AI with only this explicit source", async () => {
    preferences.excludedMemoNames = ["memos/other", "memos/one"];
    await renderActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "恢复在回顾与漫步中出现" }));
    expect(mutate).toHaveBeenCalledWith({ excludedMemoNames: ["memos/other"] }, expect.any(Object));
    fireEvent.click(screen.getByRole("button", { name: "记录选项" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "用 AI 看看这条" }));
    expect(screen.getByRole("status")).toHaveTextContent("/journal/insights?memo=memos%2Fone");
  });
});

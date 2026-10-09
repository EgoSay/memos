import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import JournalSharedContent from "@/components/JournalSharedContent";
import { type JournalShare, shareChanges, sharedMediaKind } from "@/lib/journal-share";
import JournalShared from "@/pages/JournalShared";
import JournalShares from "@/pages/JournalShares";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/hooks/useCurrentUser", () => ({ default: () => ({ name: "users/me" }) }));
vi.mock("@/hooks/useJournalQueries", () => ({
  journalRequest: request,
  useJournalPreferences: () => ({ data: { timezone: "Asia/Shanghai" } }),
}));
vi.mock("@/components/JournalSourcePicker", () => ({
  default: ({ onChange }: { onChange: (names: string[]) => void }) => (
    <button type="button" onClick={() => onChange(["memos/a"])}>
      选择一条原文
    </button>
  ),
}));

const item = { memoName: "memos/a", content: "今天散了步。", createdTs: 1_700_000_000, media: [] };
const share: JournalShare = {
  id: "one",
  title: "秋日",
  mode: "fixed",
  filter: { from: "", to: "", timezone: "Asia/Shanghai", tags: [], tagMode: "any", includeSubtags: false, memoNames: ["memos/a"] },
  excludedMemoNames: [],
  expiresTs: 0,
  createdTs: 1_700_000_000,
  paused: false,
  version: 4,
  hasPasscode: false,
  url: "/s/opaque",
};
const ownerPage = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
      <MemoryRouter>
        <JournalShares />
      </MemoryRouter>
    </QueryClientProvider>,
  );
const publicPage = () =>
  render(
    <MemoryRouter initialEntries={["/s/opaque"]}>
      <Routes>
        <Route path="/s/:token" element={<JournalShared />} />
      </Routes>
    </MemoryRouter>,
  );
beforeEach(() => request.mockReset());
afterEach(() => vi.unstubAllGlobals());

describe("sharing publication boundaries", () => {
  it("requires a real preview before publishing and binds publication to its digest", async () => {
    request.mockImplementation(async (path: string) =>
      path === "/shares/preview" ? { items: [item], previewDigest: "reviewed-digest" } : [],
    );
    ownerPage();
    fireEvent.click(screen.getByRole("button", { name: "新建" }));
    fireEvent.click(screen.getByRole("button", { name: "选择一条原文" }));
    expect(screen.queryByRole("button", { name: "创建分享链接" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "预览访客会看到的内容" }));
    fireEvent.click(await screen.findByRole("button", { name: "创建分享链接" }));
    await waitFor(() => expect(request.mock.calls.some(([path, options]) => path === "/shares" && options?.method === "POST")).toBe(true));
    const [, options] = request.mock.calls.find(([path, options]) => path === "/shares" && options?.method === "POST")!;
    expect(JSON.parse(options.body)).toMatchObject({ mode: "fixed", previewDigest: "reviewed-digest", filter: { memoNames: ["memos/a"] } });
  });
  it("invalidates the publication preview when selection settings change", async () => {
    request.mockImplementation(async (path: string) => (path === "/shares/preview" ? { items: [item], previewDigest: "old" } : []));
    ownerPage();
    fireEvent.click(screen.getByRole("button", { name: "新建" }));
    fireEvent.click(screen.getByRole("button", { name: "选择一条原文" }));
    fireEvent.click(screen.getByRole("button", { name: "预览访客会看到的内容" }));
    await screen.findByRole("button", { name: "创建分享链接" });
    fireEvent.click(screen.getByRole("radio", { name: "跟随范围更新" }));
    expect(screen.queryByRole("button", { name: "创建分享链接" })).not.toBeInTheDocument();
    expect(screen.getByText("范围已修改，请重新预览")).toBeInTheDocument();
  });
  it("pauses access without updating snapshot text or members", async () => {
    request.mockResolvedValue([share]);
    ownerPage();
    fireEvent.click(await screen.findByRole("button", { name: "停用" }));
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith("/shares/one", { method: "PATCH", body: JSON.stringify({ version: 4, paused: true }) }),
    );
  });
  it("compares actual saved snapshot content and membership for update review", () => {
    expect(
      shareChanges(
        [item, { ...item, memoName: "memos/deleted" }],
        [
          { ...item, content: "修改后的内容" },
          { ...item, memoName: "memos/new" },
        ],
      ),
    ).toEqual({ added: 1, removed: 1, changed: 1 });
  });
});

describe("visitor content isolation", () => {
  it("opens protected content only after the visitor submits the entered passcode", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response("{}", { status: 404 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ title: "秋日", items: [item] }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    publicPage();
    fireEvent.change(await screen.findByLabelText("提取码"), { target: { value: "test-code" } });
    expect(fetch).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "打开分享" }));
    expect(await screen.findByText("今天散了步。")).toBeInTheDocument();
    expect(fetch.mock.calls[1][1].body).toBe(JSON.stringify({ passcode: "test-code" }));
    expect(fetch.mock.calls[1][1].credentials).toBe("omit");
  });

  it("never executes HTML or fetches embedded private or external content", () => {
    const { container } = render(
      <JournalSharedContent
        items={[
          {
            ...item,
            content: '<img src="https://tracker.test/pixel"> ![secret](/file/private.png) [other](/memos/secret)',
            media: [
              { id: "photo", filename: "照片", type: "image/png", content: "c2FmZQ==" },
              { id: "script", filename: "bad.svg", type: "image/svg+xml", content: "c2NyaXB0" },
            ],
          },
        ]}
      />,
    );
    expect(container.querySelectorAll("img")).toHaveLength(1);
    expect(container.querySelector("img")).toHaveAttribute("src", "data:image/png;base64,c2FmZQ==");
    expect(container.querySelectorAll("a, iframe, script")).toHaveLength(0);
    expect(sharedMediaKind("text/html")).toBeUndefined();
    expect(sharedMediaKind("image/svg+xml")).toBeUndefined();
  });
  it("loads a link without session credentials and clears visible content when access is revoked", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ title: "秋日", items: [item] }), { status: 200 }))
      .mockResolvedValueOnce(new Response("{}", { status: 404 }));
    vi.stubGlobal("fetch", fetch);
    publicPage();
    expect(await screen.findByText("今天散了步。")).toBeInTheDocument();
    expect(fetch.mock.calls[0][1]).toMatchObject({
      credentials: "omit",
      cache: "no-store",
      referrerPolicy: "no-referrer",
      body: JSON.stringify({ passcode: "" }),
    });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    fireEvent(document, new Event("visibilitychange"));
    await waitFor(() => expect(screen.queryByText("今天散了步。")).not.toBeInTheDocument());
    expect(await screen.findByLabelText("提取码")).toBeInTheDocument();
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  });
});

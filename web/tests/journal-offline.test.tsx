import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createInitialState } from "@/components/MemoEditor/state";
import OfflineJournal from "@/components/OfflineJournal";
import { forgetJournalOwner, readJournalOwner, rememberJournalOwner } from "@/lib/journal-device";

const { drafts } = vi.hoisted(() => ({ drafts: { list: vi.fn(), save: vi.fn(), remove: vi.fn() } }));
vi.mock("@/lib/journal-drafts", () => ({
  journalDrafts: drafts,
  restoreJournalState: (draft: { state: unknown }) => draft.state,
}));
const owner = { name: "users/one", username: "本地用户" };

beforeEach(() => {
  vi.clearAllMocks();
  drafts.list.mockResolvedValue([]);
  drafts.save.mockResolvedValue(undefined);
  drafts.remove.mockResolvedValue(undefined);
});

describe("cold offline device drafts", () => {
  it("does not read any local drafts without the remembered owner", () => {
    render(<OfflineJournal />);
    expect(drafts.list).not.toHaveBeenCalled();
    expect(screen.queryByRole("textbox", { name: "离线记录内容" })).not.toBeInTheDocument();
    expect(screen.getByText(/已退出登录的本地记录不会在此显示/)).toBeInTheDocument();
  });

  it("reads only this owner's offline draft and keeps the composer key isolated", async () => {
    drafts.list.mockResolvedValue([
      { key: "home:users/one", kind: "draft", state: { ...createInitialState(), content: "首页草稿保持独立" } },
      { key: "offline:users/one", kind: "draft", state: { ...createInitialState(), content: "离线草稿" } },
    ]);
    render(<OfflineJournal owner={owner} />);
    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue("离线草稿"));
    expect(drafts.list).toHaveBeenCalledWith("users/one");
    expect(drafts.save).toHaveBeenCalledWith("offline:users/one", "users/one", expect.objectContaining({ content: "离线草稿" }), undefined);
  });

  it("keeps the exact input and draft on a failed pending save", async () => {
    drafts.save.mockImplementation(async (_key: string, _owner: string, _state: unknown, _options: unknown, kind?: string) => {
      if (kind === "pending") throw new Error("storage full");
    });
    render(<OfflineJournal owner={owner} />);
    await waitFor(() => expect(screen.getByRole("textbox")).toBeEnabled());
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "断网时的一句话" } });
    fireEvent.click(screen.getByRole("button", { name: "保存到此设备" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "保存到此设备" })).toBeEnabled());
    expect(screen.getByRole("textbox")).toHaveValue("断网时的一句话");
    expect(screen.getByRole("alert")).toHaveTextContent("保存未完成");
    expect(drafts.remove).not.toHaveBeenCalled();
    expect(drafts.save.mock.calls.some((args) => args[4] === "pending")).toBe(true);
  });

  it("clears the composer only after durable pending save and removes only its own key", async () => {
    render(<OfflineJournal owner={owner} />);
    await waitFor(() => expect(screen.getByRole("textbox")).toBeEnabled());
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "这条已经保存在设备" } });
    fireEvent.click(screen.getByRole("button", { name: "保存到此设备" }));
    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue(""));
    const saved = drafts.save.mock.calls.find((args) => args[4] === "pending");
    expect(saved?.[0]).toMatch(/^pending:users\/one:/);
    expect(saved?.[1]).toBe(owner.name);
    expect(saved?.[2].content).toBe("这条已经保存在设备");
    expect(drafts.remove).toHaveBeenCalledExactlyOnceWith("offline:users/one");
  });

  it("retains current input and shows the failure when switching drafts cannot save", async () => {
    drafts.list.mockResolvedValue([{ key: "home:users/one", kind: "draft", state: { ...createInitialState(), content: "另一个草稿" } }]);
    render(<OfflineJournal owner={owner} />);
    await waitFor(() => expect(screen.getByRole("textbox")).toBeEnabled());
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "正在写的内容" } });
    fireEvent.click(screen.getByText("本地草稿与待同步记录"));
    drafts.save.mockRejectedValue(new Error("storage blocked"));
    fireEvent.click(screen.getByRole("button", { name: "继续写" }));
    await waitFor(() => expect(screen.getByRole("textbox")).toBeEnabled());
    expect(screen.getByRole("textbox")).toHaveValue("正在写的内容");
    expect(screen.getByRole("alert")).toHaveTextContent("切换草稿前未能完成保存");
    expect(drafts.remove).not.toHaveBeenCalled();
  });
});

describe("local owner marker", () => {
  it("contains only an identity hint and disappears on sign-out", () => {
    rememberJournalOwner(owner);
    expect(readJournalOwner()).toEqual(owner);
    forgetJournalOwner();
    expect(readJournalOwner()).toBeUndefined();
  });
  it("fails closed for corrupt or invalid identities", () => {
    localStorage.setItem("memos-journal-device-owner", "invalid-json");
    expect(readJournalOwner()).toBeUndefined();
    localStorage.setItem("memos-journal-device-owner", JSON.stringify({ name: "users/a/other", username: "bad" }));
    expect(readJournalOwner()).toBeUndefined();
    forgetJournalOwner();
  });
});

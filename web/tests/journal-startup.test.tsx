import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import JournalStartup from "@/components/JournalStartup";

const { owner, offline } = vi.hoisted(() => ({ owner: vi.fn(), offline: vi.fn() }));
vi.mock("@/lib/journal-device", () => ({ readJournalOwner: owner }));
vi.mock("@/components/OfflineJournal", () => ({
  default: (props: { owner?: { name: string } }) => {
    offline(props);
    return <div>device drafts: {props.owner?.name ?? "no local access"}</div>;
  },
}));

const start = () =>
  render(
    <JournalStartup>
      <textarea aria-label="verified editor" />
    </JournalStartup>,
  );

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("location", { pathname: "/" });
  vi.stubGlobal("navigator", { onLine: true });
  owner.mockReturnValue({ name: "users/me", username: "owner" });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("journal cold startup", () => {
  it("uses local drafts when the server is unavailable even though the device reports online", async () => {
    const fetch = vi.fn().mockRejectedValue(new TypeError("network unavailable"));
    vi.stubGlobal("fetch", fetch);
    start();
    expect(await screen.findByText("device drafts: users/me")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith("/api/v1/instance/profile", expect.objectContaining({ credentials: "omit", cache: "no-store" }));
    expect(fetch.mock.calls[0][1].headers).toBeUndefined();
  });

  it("leaves explicit 401 responses to the real authentication flow", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 401 })));
    start();
    expect(await screen.findByRole("textbox", { name: "verified editor" })).toBeInTheDocument();
    expect(offline).not.toHaveBeenCalled();
  });

  it.each([
    "/s/token",
    "/memos/shares/token",
    "/memos/record?share_token=token",
  ])("never reads local identity or drafts from the public %s entry", async (path) => {
    const url = new URL(path, "https://journal.test");
    vi.stubGlobal("location", { pathname: url.pathname, search: url.search });
    vi.stubGlobal("navigator", { onLine: false });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    start();
    expect(screen.getByRole("textbox")).toBeInTheDocument();
    expect(owner).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
    expect(offline).not.toHaveBeenCalled();
  });

  it("does not unmount or erase an editor that is already running when connectivity changes", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    start();
    const input = await screen.findByRole("textbox");
    fireEvent.change(input, { target: { value: "正在写，不能因为断网消失" } });
    vi.stubGlobal("navigator", { onLine: false });
    fireEvent(window, new Event("offline"));
    expect(screen.getByRole("textbox")).toBe(input);
    expect(input).toHaveValue("正在写，不能因为断网消失");
    expect(fetch).toHaveBeenCalledOnce();
    expect(offline).not.toHaveBeenCalled();
  });

  it("does not inspect device drafts after an explicit logout removes the marker", () => {
    owner.mockReturnValue(undefined);
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    start();
    expect(screen.getByRole("textbox")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
    expect(offline).not.toHaveBeenCalled();
  });

  it("passes no owner access to the offline screen after sign-out", () => {
    owner.mockReturnValue(undefined);
    vi.stubGlobal("navigator", { onLine: false });
    start();
    expect(screen.getByText("device drafts: no local access")).toBeInTheDocument();
    expect(offline).toHaveBeenCalledWith({ owner: undefined });
  });

  it("aborts a hanging server probe after four seconds and releases it on unmount", async () => {
    vi.useFakeTimers();
    const fetch = vi.fn().mockImplementation(
      (_url: string, options: RequestInit) =>
        new Promise((_resolve, reject) => {
          options.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
        }),
    );
    vi.stubGlobal("fetch", fetch);
    const view = start();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });
    expect(screen.getByText("device drafts: users/me")).toBeInTheDocument();
    expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("falls back on a server failure rather than presenting an empty login screen", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 503 })));
    start();
    await waitFor(() => expect(offline).toHaveBeenCalled());
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });
});

import { fireEvent, render, screen } from "@testing-library/react";
import { Link, MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { JournalSessionProvider, useJournalSession } from "@/contexts/JournalSessionContext";

vi.mock("@/hooks/useCurrentUser", () => ({ default: () => ({ name: "users/me" }) }));

function ReaderProbe() {
  const { reviewDay, setReviewDay, wander, setWander } = useJournalSession();
  return (
    <>
      <output>
        {reviewDay?.date ?? "no-review"} / {wander?.history.join(",") ?? "no-wander"}
      </output>
      <button type="button" onClick={() => setReviewDay({ date: "2026-10-03", timezone: "Asia/Shanghai" })}>
        freeze review
      </button>
      <button
        type="button"
        onClick={() => setWander({ date: "2026-10-03", timezone: "Asia/Shanghai", names: ["a", "b"], history: ["a", "b"], index: 1 })}
      >
        freeze wander
      </button>
      <Link to="/memos/a">original record</Link>
      <Link to="/journal/review">review</Link>
      <Link to="/journal/wander">wander</Link>
      <Link to="/journal">explore</Link>
    </>
  );
}

describe("quiet reading session", () => {
  it("keeps the frozen review day across a detail detour and resets it after leaving", () => {
    render(
      <MemoryRouter initialEntries={["/journal/review"]}>
        <JournalSessionProvider>
          <ReaderProbe />
        </JournalSessionProvider>
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("button", { name: "freeze review" }));
    fireEvent.click(screen.getByRole("link", { name: "original record" }));
    expect(screen.getByRole("status")).toHaveTextContent("2026-10-03");
    fireEvent.click(screen.getByRole("link", { name: "review" }));
    expect(screen.getByRole("status")).toHaveTextContent("2026-10-03");
    fireEvent.click(screen.getByRole("link", { name: "explore" }));
    expect(screen.getByRole("status")).toHaveTextContent("no-review");
  });

  it("retains actual wander history across detail and starts fresh for a new journey", () => {
    render(
      <MemoryRouter initialEntries={["/journal/wander"]}>
        <JournalSessionProvider>
          <ReaderProbe />
        </JournalSessionProvider>
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("button", { name: "freeze wander" }));
    fireEvent.click(screen.getByRole("link", { name: "original record" }));
    fireEvent.click(screen.getByRole("link", { name: "wander" }));
    expect(screen.getByRole("status")).toHaveTextContent("a,b");
    fireEvent.click(screen.getByRole("link", { name: "explore" }));
    expect(screen.getByRole("status")).toHaveTextContent("no-wander");
  });
});

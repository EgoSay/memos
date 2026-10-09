import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SIDEBAR_ROW_BOX_CLASSES, SIDEBAR_ROW_COUNT_RAIL_CLASSES, SIDEBAR_ROW_SLOT_CLASSES } from "@/components/AppSidebar/SidebarRow";
import { SIDEBAR_SECTION_ACTION_ICON_CLASSES } from "@/components/AppSidebar/SidebarSection";
import TagsSection from "@/components/AppSidebar/TagsSection";
import { MemoFilterProvider, parseFilterQuery } from "@/contexts/MemoFilterContext";

vi.mock("@/utils/i18n", () => ({ useTranslate: () => (key: string) => key }));

describe("TagsSection", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("keeps tags discoverable when no records have tags, without asking to add one", () => {
    render(
      <MemoryRouter>
        <MemoFilterProvider>
          <TagsSection tagCount={{}} scope="home" />
        </MemoFilterProvider>
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { name: "common.tags" })).toBeInTheDocument();
    expect(screen.getByText("common.empty-placeholder")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it.each(["/", "/calendar/2026/10", "/map"])("applies and clears a tag filter on %s without losing other filters", async (path) => {
    const onSelect = vi.fn();
    const LocationProbe = () => {
      const location = useLocation();
      return <output data-testid="filter-location">{JSON.stringify({ pathname: location.pathname, search: location.search })}</output>;
    };
    render(
      <MemoryRouter initialEntries={[`${path}?creator=alice&filter=contentSearch%3Aflowers`]}>
        <MemoFilterProvider>
          <TagsSection tagCount={{ "life/walks": 3 }} scope="alice" onSelect={onSelect} />
          <LocationProbe />
        </MemoFilterProvider>
      </MemoryRouter>,
    );
    const tag = screen.getByRole("button", { name: "#life/walks, setting.tags.used-count" });
    expect(within(tag).getByText("3")).toBeInTheDocument();
    fireEvent.click(tag);
    await waitFor(() => expect(tag).toHaveAttribute("aria-pressed", "true"));
    const location = JSON.parse(screen.getByTestId("filter-location").textContent || "{}");
    expect(location.pathname).toBe(path);
    expect(new URLSearchParams(location.search).get("creator")).toBe("alice");
    expect(parseFilterQuery(new URLSearchParams(location.search).get("filter"))).toEqual([
      { factor: "contentSearch", value: "flowers" },
      { factor: "tagSearch", value: "life/walks" },
    ]);
    fireEvent.click(tag);
    await waitFor(() => expect(tag).not.toHaveAttribute("aria-pressed"));
    const cleared = JSON.parse(screen.getByTestId("filter-location").textContent || "{}");
    expect(parseFilterQuery(new URLSearchParams(cleared.search).get("filter"))).toEqual([{ factor: "contentSearch", value: "flowers" }]);
    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it("keeps the title count-free and uses the shared section action grammar", () => {
    render(
      <MemoryRouter>
        <MemoFilterProvider>
          <TagsSection tagCount={{ a: 2, "a/b": 1 }} scope="home" />
        </MemoFilterProvider>
      </MemoryRouter>,
    );

    const heading = screen.getByRole("heading", { name: "common.tags", level: 2 });
    expect(heading.parentElement).toHaveTextContent(/^common.tags$/);

    const trigger = screen.getByRole("button", { name: "common.tags: common.more" });
    expect(trigger).toHaveClass("size-6", "rounded-md", "text-muted-foreground/70", "hover:bg-muted/60", "hover:text-foreground");
    expect(trigger.querySelector("svg")).toHaveClass(SIDEBAR_SECTION_ACTION_ICON_CLASSES);
    expect(screen.queryByRole("button", { name: "common.tags: memo.layout-list" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitemcheckbox")).not.toBeInTheDocument();
  });

  it("switches layouts from the menu, closes it, and persists the preference", async () => {
    const onSelect = vi.fn();
    render(
      <MemoryRouter>
        <MemoFilterProvider>
          <TagsSection tagCount={{ a: 2, "a/b": 1 }} scope="home" onSelect={onSelect} />
        </MemoFilterProvider>
      </MemoryRouter>,
    );

    const trigger = screen.getByRole("button", { name: "common.tags: common.more" });
    fireEvent.click(trigger);
    const treeMode = await screen.findByRole("menuitemcheckbox", { name: "common.tree-mode" });
    expect(treeMode).toHaveAttribute("aria-checked", "false");
    fireEvent.click(treeMode);

    expect(screen.getByRole("tree")).toBeInTheDocument();
    expect(localStorage.getItem("tag-view-as-tree")).toBe("true");
    await waitFor(() => expect(screen.queryByRole("menuitemcheckbox")).not.toBeInTheDocument());
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger);
    const checkedTreeMode = await screen.findByRole("menuitemcheckbox", { name: "common.tree-mode" });
    expect(checkedTreeMode).toHaveAttribute("aria-checked", "true");
    fireEvent.click(checkedTreeMode);

    expect(screen.queryByRole("tree")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "#a/b, setting.tags.used-count" })).toBeInTheDocument();
    expect(localStorage.getItem("tag-view-as-tree")).toBe("false");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("reflects an existing tree preference when opening the menu", async () => {
    localStorage.setItem("tag-view-as-tree", "true");
    render(
      <MemoryRouter>
        <MemoFilterProvider>
          <TagsSection tagCount={{ a: 2, "a/b": 1 }} scope="home" />
        </MemoFilterProvider>
      </MemoryRouter>,
    );

    expect(screen.getByRole("tree")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "common.tags: common.more" }));
    expect(await screen.findByRole("menuitemcheckbox", { name: "common.tree-mode" })).toHaveAttribute("aria-checked", "true");
  });

  it("keeps flat rows on the shared row grammar with a trailing count rail", () => {
    render(
      <MemoryRouter>
        <MemoFilterProvider>
          <TagsSection tagCount={{ alpha: 2, "a/very-long-tag-path": 1 }} scope="home" />
        </MemoFilterProvider>
      </MemoryRouter>,
    );

    const alpha = screen.getByText("alpha");
    const alphaButton = alpha.closest("button") as HTMLButtonElement;
    const path = alpha.parentElement?.parentElement as HTMLSpanElement;
    const count = screen.getByText("2");

    expect(alphaButton).toHaveClass(...SIDEBAR_ROW_BOX_CLASSES.split(" "));
    // The # sits in the same fixed slot the tree uses, so switching modes keeps it in place.
    expect(alphaButton.firstElementChild).toHaveClass(...SIDEBAR_ROW_SLOT_CLASSES.split(" "));
    expect(path).toHaveClass("truncate", "text-start");
    expect(path).not.toContainElement(count);
    expect(count).toHaveClass(...SIDEBAR_ROW_COUNT_RAIL_CLASSES.split(" "));
  });
});

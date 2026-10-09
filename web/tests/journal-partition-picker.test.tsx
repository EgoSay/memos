import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import PartitionPicker from "@/components/Journal/PartitionPicker";

const queries = vi.hoisted(() => ({ partitions: vi.fn(), targets: vi.fn() }));
vi.mock("@/hooks/useJournalPartitionQueries", () => ({
  useJournalPartitions: queries.partitions,
  useJournalPartitionTargets: queries.targets,
}));

const partitions = [
  { id: "a", name: "生活", version: 1 },
  { id: "b", name: "随笔", version: 1 },
];
const cachedTargets = (targets: Array<{ name: string; enabled: boolean }> = []) => ({
  data: { targets },
  isPending: false,
  isError: false,
  isSuccess: true,
});

beforeEach(() => {
  queries.partitions.mockReturnValue({ data: { partitions }, isPending: false });
  queries.targets.mockReturnValue(cachedTargets());
});

describe("optional journal partition picker", () => {
  it.each([undefined, { partitions: [] }])("does not reserve a toolbar placeholder without any partition", (data) => {
    queries.partitions.mockReturnValue({ data, isPending: !data });
    const onSyncChange = vi.fn();
    const { container } = render(<PartitionPicker value="" onChange={vi.fn()} onSyncChange={onSyncChange} />);
    expect(container).toBeEmptyDOMElement();
    expect(onSyncChange).toHaveBeenLastCalledWith(false);
  });

  it("offers an icon-only optional control and selects a real partition through its menu", async () => {
    const onChange = vi.fn();
    render(<PartitionPicker value="" onChange={onChange} />);
    const trigger = screen.getByRole("button", { name: "记录所属分区：选择分区（可选）" });
    expect(trigger.textContent).toBe("");
    expect(trigger).toHaveAttribute("title", "选择分区（可选）");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("menuitem", { name: "随笔" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith("b");
    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
  });

  it("shows the selected partition and lets the owner remove the assignment", async () => {
    const onChange = vi.fn();
    render(<PartitionPicker value="a" onChange={onChange} />);
    const trigger = screen.getByRole("button", { name: "记录所属分区：生活" });
    expect(trigger).toHaveTextContent("生活");
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("menuitem", { name: "不放入分区" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith("");
  });

  it("keeps an existing assignment visible while partition names are still loading", () => {
    queries.partitions.mockReturnValue({ data: undefined, isPending: true });
    render(<PartitionPicker value="a" onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "记录所属分区：原分区" })).toBeDisabled();
  });

  it("reports the sync flag again when switching directly between two already cached partitions", () => {
    queries.targets.mockImplementation((id: string) => {
      expect(["a", "b"]).toContain(id);
      return cachedTargets();
    });
    const onSyncChange = vi.fn();
    const onChange = vi.fn();
    const { rerender } = render(<PartitionPicker value="a" onChange={onChange} onSyncChange={onSyncChange} />);
    expect(onSyncChange).toHaveBeenLastCalledWith(false);
    onSyncChange.mockClear();
    rerender(<PartitionPicker value="b" onChange={onChange} onSyncChange={onSyncChange} />);
    expect(onSyncChange).toHaveBeenCalledExactlyOnceWith(false);
    expect(screen.getByRole("button", { name: "记录所属分区：随笔" })).toBeInTheDocument();
  });

  it("discloses only enabled destinations and signals that complete saving will synchronize", () => {
    queries.targets.mockReturnValue(
      cachedTargets([
        { name: "个人博客", enabled: true },
        { name: "已停用的社媒", enabled: false },
        { name: "个人相册", enabled: true },
      ]),
    );
    const onSyncChange = vi.fn();
    render(<PartitionPicker value="a" onChange={vi.fn()} onSyncChange={onSyncChange} />);
    expect(screen.getByText("完整保存后同步到：个人博客、个人相册")).toBeInTheDocument();
    expect(screen.queryByText(/已停用的社媒/)).not.toBeInTheDocument();
    expect(onSyncChange).toHaveBeenLastCalledWith(true);
  });

  it("discloses a suspended assignment without suggesting its enabled targets will receive this save", () => {
    queries.targets.mockReturnValue(cachedTargets([{ name: "个人博客", enabled: true }]));
    const onSyncChange = vi.fn();
    render(<PartitionPicker value="a" onChange={vi.fn()} onSyncChange={onSyncChange} suspended />);
    expect(screen.getByText("同步已暂停，重新选择分区后启用。")).toBeInTheDocument();
    expect(screen.queryByText(/完整保存后同步到/)).not.toBeInTheDocument();
    expect(onSyncChange).toHaveBeenLastCalledWith(false);
  });

  it.each(["loading", "error"])("shows %s feedback without inventing a synchronization destination", (state) => {
    queries.targets.mockReturnValue({ data: undefined, isPending: state === "loading", isError: state === "error", isSuccess: false });
    const onSyncChange = vi.fn();
    render(<PartitionPicker value="a" onChange={vi.fn()} onSyncChange={onSyncChange} />);
    expect(onSyncChange).toHaveBeenLastCalledWith(false);
    if (state === "loading") expect(screen.getByText("正在确认同步设置…")).toBeInTheDocument();
    else expect(screen.getByRole("alert")).toHaveTextContent("暂时无法读取同步设置。");
    expect(screen.queryByText(/完整保存后同步到/)).not.toBeInTheDocument();
  });

  it("cannot open or change the assignment while saving disables the picker", () => {
    const onChange = vi.fn();
    render(<PartitionPicker value="a" onChange={onChange} disabled />);
    const trigger = screen.getByRole("button", { name: "记录所属分区：生活" });
    expect(trigger).toBeDisabled();
    fireEvent.click(trigger);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("cannot change an already open menu when saving starts", async () => {
    const onChange = vi.fn();
    const { rerender } = render(<PartitionPicker value="a" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "记录所属分区：生活" }));
    await screen.findByRole("menuitem", { name: "随笔" });
    rerender(<PartitionPicker value="a" onChange={onChange} disabled />);
    const option = screen.queryByRole("menuitem", { name: "随笔" });
    if (option) fireEvent.click(option);
    expect(onChange).not.toHaveBeenCalled();
  });
});

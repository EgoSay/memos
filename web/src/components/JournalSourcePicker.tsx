import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { memoServiceClient } from "@/connect";
import useCurrentUser from "@/hooks/useCurrentUser";
import { useJournalPreferences } from "@/hooks/useJournalQueries";
import { memoKeys } from "@/hooks/useMemoQueries";
import { combineCELFilters } from "@/lib/cel-filter";
import { journalDayRange } from "@/lib/journal";
import { buildMemoCreatorFilter } from "@/lib/resource-names";
import { State } from "@/types/proto/api/v1/common_pb";

/** Explicit source selection; changing a query never expands the selected set. */
export default function JournalSourcePicker({
  selected,
  onChange,
  disabled = false,
  allowArchived = true,
}: {
  selected: string[];
  onChange: (names: string[]) => void;
  disabled?: boolean;
  allowArchived?: boolean;
}) {
  const user = useCurrentUser();
  const preferences = useJournalPreferences();
  const timezone = preferences.data?.timezone || "Asia/Shanghai";
  const [draftQuery, setDraftQuery] = useState("");
  const [draftFrom, setDraftFrom] = useState("");
  const [draftTo, setDraftTo] = useState("");
  const [query, setQuery] = useState({ text: "", from: "", to: "" });
  const [dateError, setDateError] = useState("");
  const [archived, setArchived] = useState(false);
  const from = query.from ? journalDayRange(query.from, timezone)?.start : undefined;
  const to = query.to ? journalDayRange(query.to, timezone)?.end : undefined;
  const records = useInfiniteQuery({
    queryKey: [...memoKeys.lists(), "journal-source-picker", user?.name, query, archived, timezone],
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      memoServiceClient.listMemos(
        {
          pageToken: pageParam,
          pageSize: 30,
          state: archived ? State.ARCHIVED : State.NORMAL,
          filter: combineCELFilters(
            buildMemoCreatorFilter(user?.name ?? ""),
            query.text ? `content.contains(${JSON.stringify(query.text)})` : undefined,
            from !== undefined ? `create_time >= ${from}` : undefined,
            to !== undefined ? `create_time < ${to}` : undefined,
          ),
          orderBy: "create_time desc",
        },
        { signal },
      ),
    getNextPageParam: (page) => page.nextPageToken || undefined,
    enabled: Boolean(user),
  });
  return (
    <section className="space-y-4 [&_button]:min-h-11" aria-label="选择原始记录">
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (draftFrom && draftTo && draftFrom > draftTo) {
            setDateError("结束日期不能早于开始日期。");
            return;
          }
          setDateError("");
          setQuery({ text: draftQuery.trim(), from: draftFrom, to: draftTo });
        }}
      >
        <div className="flex gap-2">
          <Input
            aria-label="查找记录"
            placeholder="查找一段文字…"
            value={draftQuery}
            onChange={(e) => setDraftQuery(e.target.value)}
            disabled={disabled}
          />
          <Button type="submit" variant="outline" disabled={disabled}>
            查找
          </Button>
        </div>
        <details className="text-sm text-muted-foreground">
          <summary className="cursor-pointer py-2">按日期范围查找</summary>
          <div className="grid gap-3 py-3 sm:grid-cols-2">
            <label className="space-y-2">
              <span>从哪一天</span>
              <Input type="date" value={draftFrom} disabled={disabled} onChange={(event) => setDraftFrom(event.target.value)} />
            </label>
            <label className="space-y-2">
              <span>到哪一天（含当天）</span>
              <Input type="date" value={draftTo} disabled={disabled} onChange={(event) => setDraftTo(event.target.value)} />
            </label>
          </div>
          <p className="text-xs">按记录日期 · {timezone}，填写后点击查找。</p>
        </details>
        {dateError && (
          <p role="alert" className="text-sm text-destructive">
            {dateError}
          </p>
        )}
      </form>
      <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
        <span>已选 {selected.length} 条</span>
        {allowArchived && (
          <label className="flex min-h-11 cursor-pointer items-center gap-2">
            <input type="checkbox" checked={archived} disabled={disabled} onChange={(e) => setArchived(e.target.checked)} />
            查找归档记录
          </label>
        )}
        {selected.length > 0 && (
          <Button variant="ghost" disabled={disabled} onClick={() => onChange([])}>
            清空选择
          </Button>
        )}
      </div>
      {records.isPending && <p role="status">正在读取记录…</p>}
      {records.isError && (
        <div role="alert">
          <p>暂时无法打开记录。</p>
          <Button variant="outline" onClick={() => records.refetch()}>
            重试
          </Button>
        </div>
      )}
      <div className="max-h-96 space-y-2 overflow-y-auto rounded-lg border border-border/60 p-2">
        {records.data?.pages
          .flatMap((page) => page.memos)
          .filter((memo) => memo.creator === user?.name && !memo.parent)
          .map((memo) => (
            <label key={memo.name} className="flex cursor-pointer items-start gap-3 rounded-md p-3 hover:bg-muted/40">
              <input
                className="mt-1 size-4"
                type="checkbox"
                checked={selected.includes(memo.name)}
                disabled={disabled}
                onChange={(e) =>
                  onChange(e.target.checked ? [...new Set([...selected, memo.name])] : selected.filter((name) => name !== memo.name))
                }
              />
              <span className="min-w-0">
                <time className="mb-1 block text-xs text-muted-foreground">
                  {memo.createTime ? timestampDate(memo.createTime).toLocaleDateString("zh-CN", { timeZone: timezone }) : "日期未知"}
                </time>
                <span className="line-clamp-3 whitespace-pre-wrap text-sm leading-relaxed">{memo.content || "照片或声音记录"}</span>
              </span>
            </label>
          ))}
        {records.data?.pages[0]?.memos.length === 0 && <p className="p-5 text-sm text-muted-foreground">没有找到记录。</p>}
      </div>
      {records.hasNextPage && (
        <Button variant="ghost" disabled={records.isFetchingNextPage} onClick={() => records.fetchNextPage()}>
          再往前找找
        </Button>
      )}
    </section>
  );
}

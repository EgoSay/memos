import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ArrowLeftIcon } from "lucide-react";
import { Link, useParams } from "react-router-dom";
import JournalMemo from "@/components/JournalMemo";
import { Button } from "@/components/ui/button";
import useCurrentUser from "@/hooks/useCurrentUser";
import { useJournalPreferences } from "@/hooks/useJournalQueries";
import { useInfiniteMemos } from "@/hooks/useMemoQueries";
import { buildTimestampRangeFilter } from "@/lib/calendar-utils";
import { combineCELFilters } from "@/lib/cel-filter";
import { journalDayRange } from "@/lib/journal";
import { buildMemoCreatorFilter } from "@/lib/resource-names";
import { ROUTES } from "@/router/routes";
import { State } from "@/types/proto/api/v1/common_pb";

function DayRecords({ date, timezone }: { date: string; timezone: string }) {
  const user = useCurrentUser();
  const range = journalDayRange(date, timezone);
  const filter = combineCELFilters(
    buildMemoCreatorFilter(user?.name ?? ""),
    range ? buildTimestampRangeFilter("created_ts", { startTimestamp: range.start, endTimestamp: range.end }) : "false",
  );
  const normal = useInfiniteMemos({ state: State.NORMAL, filter, pageSize: 100, orderBy: "create_time desc" }, { enabled: Boolean(range) });
  const archived = useInfiniteMemos(
    { state: State.ARCHIVED, filter, pageSize: 100, orderBy: "create_time desc" },
    { enabled: Boolean(range) },
  );
  const memos = [...(normal.data?.pages.flatMap((page) => page.memos) ?? []), ...(archived.data?.pages.flatMap((page) => page.memos) ?? [])]
    .filter((memo) => memo.creator === user?.name && !memo.parent)
    .sort(
      (a, b) => (b.createTime ? timestampDate(b.createTime).getTime() : 0) - (a.createTime ? timestampDate(a.createTime).getTime() : 0),
    );
  const pending = normal.isPending || archived.isPending;
  const failed = normal.isError || archived.isError;
  if (!range) return <p className="text-sm text-muted-foreground">这个日期无效。</p>;
  return (
    <>
      {pending && (
        <p role="status" className="py-8 text-sm text-muted-foreground">
          正在翻开记录…
        </p>
      )}
      {failed && (
        <div role="alert" className="py-5">
          <p>暂时没能读取完整记录。</p>
          <Button
            variant="outline"
            onClick={() => {
              void normal.refetch();
              void archived.refetch();
            }}
          >
            重试
          </Button>
        </div>
      )}
      {!pending && !failed && memos.length === 0 && <p className="py-8 text-sm text-muted-foreground">这一天没有记录。</p>}
      <div className="space-y-5">
        {memos.map((memo) => (
          <JournalMemo key={memo.name} memo={memo} />
        ))}
      </div>
      {(normal.hasNextPage || archived.hasNextPage) && (
        <Button
          className="mt-6"
          variant="outline"
          disabled={normal.isFetchingNextPage || archived.isFetchingNextPage}
          onClick={() => {
            if (normal.hasNextPage) void normal.fetchNextPage();
            if (archived.hasNextPage) void archived.fetchNextPage();
          }}
        >
          继续查看
        </Button>
      )}
    </>
  );
}

export default function JournalDay() {
  const { date = "" } = useParams();
  const preferences = useJournalPreferences();
  return (
    <div className="mx-auto max-w-2xl pb-12 pt-2 sm:pt-7">
      <Link
        to={ROUTES.HOME}
        className="mb-7 inline-flex min-h-11 items-center gap-2 rounded text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeftIcon className="size-4" />
        记录
      </Link>
      <h1 className="mb-2 font-serif text-3xl font-medium tracking-tight">{date}</h1>
      <p className="mb-8 text-sm text-muted-foreground">全部记录，包含已归档的片段。</p>
      {preferences.isPending && (
        <p role="status" className="text-sm text-muted-foreground">
          正在读取设置…
        </p>
      )}
      {preferences.isError && (
        <Button variant="outline" onClick={() => preferences.refetch()}>
          重试
        </Button>
      )}
      {preferences.data && <DayRecords date={date} timezone={preferences.data.timezone} />}
    </div>
  );
}

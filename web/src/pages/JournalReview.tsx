import { ArrowLeftIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import JournalMemo from "@/components/JournalMemo";
import { Button } from "@/components/ui/button";
import { useJournalSession } from "@/contexts/JournalSessionContext";
import useCurrentUser from "@/hooks/useCurrentUser";
import { type JournalPreferences, useJournalMemoDetails, useJournalPreferences, useJournalReview } from "@/hooks/useJournalQueries";
import { isJournalCandidate, journalDate } from "@/lib/journal";
import { ROUTES } from "@/router/routes";

function DailyRecords({ preferences }: { preferences: JournalPreferences }) {
  const user = useCurrentUser();
  const { reviewDay, setReviewDay } = useJournalSession();
  // Freeze the reader's day on entry; crossing midnight must never replace their page.
  const [day] = useState(() => reviewDay ?? { date: journalDate(new Date(), preferences.timezone), timezone: preferences.timezone });
  useEffect(() => {
    if (!reviewDay) setReviewDay(day);
  }, [reviewDay, setReviewDay, day]);
  const selection = useJournalReview(day.date, day.timezone);
  const details = useJournalMemoDetails(selection.data?.memoNames ?? []);
  const excluded = new Set(preferences.excludedMemoNames);
  const memos = details.flatMap((query) =>
    query.data && !query.isError && isJournalCandidate(query.data, user?.name ?? "", day.date, day.timezone, excluded) ? [query.data] : [],
  );
  const pending = selection.isPending || details.some((query) => query.isPending);
  const failed = selection.isError || details.some((query) => query.isError);
  return (
    <>
      {pending && (
        <p role="status" className="py-10 text-sm text-muted-foreground">
          正在翻开记录…
        </p>
      )}
      {failed && (
        <div role="alert" className="space-y-3 py-6">
          <p className="text-sm text-muted-foreground">暂时没能打开记录。</p>
          <Button
            variant="outline"
            onClick={() => {
              void selection.refetch();
              details.forEach((query) => {
                void query.refetch();
              });
            }}
          >
            重试
          </Button>
        </div>
      )}
      {!pending && !failed && memos.length === 0 && <p className="py-12 text-sm text-muted-foreground">这里暂时没有过去的记录。</p>}
      <div className="space-y-5">
        {memos.map((memo) => (
          <JournalMemo key={memo.name} memo={memo} />
        ))}
      </div>
    </>
  );
}

export default function JournalReview() {
  const preferences = useJournalPreferences();
  return (
    <div className="mx-auto max-w-2xl pb-12 pt-2 sm:pt-7">
      <Link
        to={ROUTES.JOURNAL}
        className="mb-7 inline-flex min-h-11 items-center gap-2 rounded text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeftIcon className="size-4" />
        随便看看
      </Link>
      <h1 className="mb-8 font-serif text-3xl font-medium tracking-tight">每日回顾</h1>
      {preferences.isPending && (
        <p role="status" className="text-sm text-muted-foreground">
          正在翻开记录…
        </p>
      )}
      {preferences.isError && (
        <Button variant="outline" onClick={() => preferences.refetch()}>
          重试
        </Button>
      )}
      {preferences.data && <DailyRecords preferences={preferences.data} />}
    </div>
  );
}

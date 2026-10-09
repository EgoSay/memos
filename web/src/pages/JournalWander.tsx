import { ArrowLeftIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import JournalMemo from "@/components/JournalMemo";
import { Button } from "@/components/ui/button";
import { useJournalSession } from "@/contexts/JournalSessionContext";
import useCurrentUser from "@/hooks/useCurrentUser";
import { type JournalPreferences, useJournalCandidates, useJournalMemoDetails, useJournalPreferences } from "@/hooks/useJournalQueries";
import { advanceJournalWander, isJournalCandidate, journalDate, startJournalWander } from "@/lib/journal";
import { ROUTES } from "@/router/routes";

function WanderRecords({ preferences }: { preferences: JournalPreferences }) {
  const user = useCurrentUser();
  const { wander, setWander } = useJournalSession();
  const [day] = useState(() => ({
    date: wander?.date ?? journalDate(new Date(), preferences.timezone),
    timezone: wander?.timezone ?? preferences.timezone,
  }));
  const candidates = useJournalCandidates(day.date, day.timezone, preferences.excludedMemoNames);
  useEffect(() => {
    if (!wander && candidates.isSuccess)
      setWander(
        startJournalWander(
          candidates.data.map((memo) => memo.name),
          day.date,
          day.timezone,
        ),
      );
  }, [wander, setWander, candidates.isSuccess, candidates.data, day]);
  const name = wander?.history[wander.index];
  const [current] = useJournalMemoDetails(name ? [name] : []);
  const memo = current?.data;
  const excluded = new Set(preferences.excludedMemoNames);
  const available = memo && !current.isError && isJournalCandidate(memo, user?.name ?? "", day.date, day.timezone, excluded);
  const eligibleNames = new Set(candidates.data?.map((memo) => memo.name));
  const exhausted = Boolean(wander && wander.names.length > 0 && wander.index >= wander.history.length);
  const loading = candidates.isPending || !wander || current?.isPending;
  const failed = candidates.isError || current?.isError;
  return (
    <>
      {failed ? (
        <div role="alert" className="space-y-3 py-8">
          <p className="text-sm text-muted-foreground">暂时没能打开记录。</p>
          <Button
            variant="outline"
            onClick={() => {
              void candidates.refetch();
              void current?.refetch();
            }}
          >
            重试
          </Button>
        </div>
      ) : loading ? (
        <p role="status" className="py-10 text-sm text-muted-foreground">
          正在翻开记录…
        </p>
      ) : available ? (
        <JournalMemo key={memo.name} memo={memo} />
      ) : (
        <p className="py-12 text-sm text-muted-foreground">
          {exhausted ? "这些记录已经翻过了。" : name ? "这条记录已移除，或暂不出现在漫步中。" : "这里暂时没有过去的记录。"}
        </p>
      )}
      {wander && wander.names.length > 0 && (
        <div className="mt-6 flex flex-wrap items-center gap-3">
          <Button
            variant="outline"
            className="min-h-11"
            disabled={wander.index === 0}
            onClick={() => setWander({ ...wander, index: wander.index - 1 })}
          >
            上一条
          </Button>
          {exhausted ? (
            <Button
              variant="outline"
              className="min-h-11"
              disabled={!candidates.isSuccess}
              onClick={() => setWander(startJournalWander([...eligibleNames], day.date, day.timezone))}
            >
              重新随便看看
            </Button>
          ) : (
            <Button
              variant="outline"
              className="min-h-11"
              disabled={Boolean(loading || failed)}
              onClick={() => setWander(advanceJournalWander(wander, eligibleNames))}
            >
              换一条
            </Button>
          )}
        </div>
      )}
    </>
  );
}

export default function JournalWander() {
  const preferences = useJournalPreferences();
  const { setWander } = useJournalSession();
  return (
    <div className="mx-auto max-w-2xl pb-12 pt-2 sm:pt-7">
      <Link
        to={ROUTES.JOURNAL}
        onClick={() => setWander(undefined)}
        className="mb-7 inline-flex min-h-11 items-center gap-2 rounded text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeftIcon className="size-4" />
        随便看看
      </Link>
      <h1 className="mb-8 font-serif text-3xl font-medium tracking-tight">随机漫步</h1>
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
      {preferences.data && <WanderRecords preferences={preferences.data} />}
    </div>
  );
}

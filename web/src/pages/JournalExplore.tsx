import { ArrowUpRightIcon, BookOpenIcon, ShuffleIcon, SparklesIcon } from "lucide-react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { useJournalSession } from "@/contexts/JournalSessionContext";
import { useJournalPreferences, useUpdateJournalPreferences } from "@/hooks/useJournalQueries";
import { ROUTES } from "@/router/routes";

const journeys = [
  { to: ROUTES.JOURNAL_REVIEW, icon: BookOpenIcon, title: "每日回顾", description: "翻开几页过去的日常。" },
  { to: ROUTES.JOURNAL_WANDER, icon: ShuffleIcon, title: "随机漫步", description: "没有顺序，随便遇见一段从前。" },
  { to: ROUTES.JOURNAL_INSIGHTS, icon: SparklesIcon, title: "AI 洞察", description: "选几条记录，换一个角度看看。" },
];

export default function JournalExplore() {
  const preferences = useJournalPreferences();
  const update = useUpdateJournalPreferences();
  const { setWander, setReviewDay } = useJournalSession();
  const deviceTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return (
    <div className="mx-auto w-full max-w-2xl pb-12 pt-5 sm:pt-10 [&_button]:min-h-11">
      <header className="mb-10 px-1">
        <h1 className="font-serif text-3xl font-medium tracking-tight">随便看看</h1>
        <p className="mt-3 text-sm leading-6 text-muted-foreground">过去的日子，就在这里。</p>
      </header>
      <div className="divide-y divide-border/65 rounded-xl border border-border/65 bg-card px-5 sm:px-7">
        {journeys.map(({ to, icon: Icon, title, description }) => (
          <Link
            key={to}
            to={to}
            onClick={() => {
              if (to === ROUTES.JOURNAL_WANDER) setWander(undefined);
              if (to === ROUTES.JOURNAL_REVIEW) setReviewDay(undefined);
            }}
            className="group flex min-h-32 items-center gap-5 rounded-sm py-6 outline-offset-4 focus-visible:outline-2"
          >
            <Icon className="size-5 shrink-0 text-muted-foreground" strokeWidth={1.5} />
            <div className="flex-1">
              <h2 className="text-lg font-medium">{title}</h2>
              <p className="mt-1.5 text-sm leading-6 text-muted-foreground">{description}</p>
            </div>
            <ArrowUpRightIcon className="size-4 text-muted-foreground/60 group-hover:text-foreground" />
          </Link>
        ))}
      </div>
      <details className="mt-10 px-1 text-sm text-muted-foreground">
        <summary className="w-fit cursor-pointer rounded py-3 focus-visible:outline-2">回顾与日历偏好</summary>
        <div className="space-y-4 py-3">
          {preferences.isPending && <p role="status">正在读取设置…</p>}
          {preferences.isError && (
            <Button variant="outline" onClick={() => preferences.refetch()}>
              重试读取设置
            </Button>
          )}
          {preferences.data && (
            <>
              <label className="flex min-h-11 items-center gap-3">
                <input
                  type="checkbox"
                  checked={preferences.data.calendarVisible}
                  disabled={update.isPending}
                  onChange={(event) => update.mutate({ calendarVisible: event.target.checked })}
                />
                显示活动日历
              </label>
              <label className="flex min-h-11 items-center gap-3">
                <input
                  type="checkbox"
                  checked={preferences.data.calendarColor}
                  disabled={update.isPending}
                  onChange={(event) => update.mutate({ calendarColor: event.target.checked })}
                />
                按记录数量着色
              </label>
              <label className="block space-y-2">
                <span>记录日期时区</span>
                <select
                  className="min-h-11 w-full rounded-md border border-border bg-background px-3 text-foreground"
                  value={preferences.data.timezone}
                  disabled={update.isPending}
                  onChange={(event) => update.mutate({ timezone: event.target.value })}
                >
                  {[
                    ...new Set([
                      preferences.data.timezone,
                      deviceTimezone,
                      "Asia/Shanghai",
                      "UTC",
                      "Asia/Tokyo",
                      "Europe/London",
                      "Europe/Paris",
                      "America/New_York",
                      "America/Los_Angeles",
                      "Australia/Sydney",
                    ]),
                  ].map((timezone) => (
                    <option key={timezone} value={timezone}>
                      {timezone}
                      {timezone === deviceTimezone ? " · 此设备" : ""}
                    </option>
                  ))}
                </select>
              </label>
              <p className="text-xs leading-6">用于日历和回顾的日期划分，不修改记录原文或时间。正在翻看的回顾保持原样，下次进入时应用。</p>
              {preferences.data.excludedMemoNames.length > 0 && (
                <div className="space-y-3">
                  <p>暂不出现在回顾与漫步中的记录</p>
                  {preferences.data.excludedMemoNames.map((name) => (
                    <div key={name} className="flex items-center justify-between gap-3">
                      <Link className="underline underline-offset-4" to={`/${name}`}>
                        打开原记录
                      </Link>
                      <Button
                        variant="ghost"
                        disabled={update.isPending}
                        onClick={() =>
                          update.mutate({ excludedMemoNames: preferences.data?.excludedMemoNames.filter((value) => value !== name) })
                        }
                      >
                        恢复出现
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
          {update.isError && (
            <p role="alert" className="text-destructive">
              设置未保存，请重试。
            </p>
          )}
        </div>
      </details>
    </div>
  );
}

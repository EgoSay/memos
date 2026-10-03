import { timestampDate } from "@bufbuild/protobuf/wkt";
import { State } from "@/types/proto/api/v1/common_pb";
import type { Memo } from "@/types/proto/api/v1/memo_service_pb";

/** A calendar date in the user's chosen zone, independent of the device's zone. */
export function journalDate(date: Date, timezone: string): string {
  const parts = new Intl.DateTimeFormat("en-CA", { timeZone: timezone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(
    date,
  );
  const part = (type: string) => parts.find((value) => value.type === type)?.value ?? "";
  return `${part("year")}-${part("month")}-${part("day")}`;
}

/** Find the real day boundaries, including 23/25 hour days, without assuming the device's offset. */
export function journalDayRange(date: string, timezone: string): { start: number; end: number } | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return undefined;
  const utc = Date.parse(`${date}T00:00:00Z`);
  if (!Number.isFinite(utc) || new Date(utc).toISOString().slice(0, 10) !== date) return undefined;
  const nextDate = new Date(utc + 86_400_000).toISOString().slice(0, 10);
  const boundary = (target: string, around: number) => {
    let low = Math.floor((around - 36 * 3_600_000) / 1000);
    let high = Math.floor((around + 36 * 3_600_000) / 1000);
    while (low < high) {
      const mid = Math.floor((low + high) / 2);
      if (journalDate(new Date(mid * 1000), timezone) < target) low = mid + 1;
      else high = mid;
    }
    return low;
  };
  return { start: boundary(date, utc), end: boundary(nextDate, utc + 86_400_000) };
}

export function isJournalCandidate(memo: Memo, owner: string, date: string, timezone: string, excluded: ReadonlySet<string>): boolean {
  return Boolean(
    memo.creator === owner &&
      memo.state === State.NORMAL &&
      !memo.parent &&
      memo.createTime &&
      journalDate(timestampDate(memo.createTime), timezone) < date &&
      !excluded.has(memo.name),
  );
}

/** Fisher-Yates gives every eligible record the same chance; the input is never changed. */
export function shuffleJournalNames(names: readonly string[], random: () => number = Math.random): string[] {
  const result = [...new Set(names)];
  for (let index = result.length - 1; index > 0; index--) {
    const target = Math.min(index, Math.max(0, Math.floor(random() * (index + 1))));
    [result[index], result[target]] = [result[target], result[index]];
  }
  return result;
}

export interface JournalWanderSession {
  names: string[];
  history: string[];
  index: number;
  date: string;
  timezone: string;
}

/** Reuse the actual forward history first; skipped/removed candidates never become a previous page. */
export function advanceJournalWander(session: JournalWanderSession, eligibleNames: ReadonlySet<string>): JournalWanderSession {
  if (session.index < session.history.length - 1) return { ...session, index: session.index + 1 };
  const seen = new Set(session.history);
  const next = session.names.find((name) => !seen.has(name) && eligibleNames.has(name));
  return { ...session, history: next ? [...session.history, next] : session.history, index: session.history.length };
}

export function startJournalWander(names: readonly string[], date: string, timezone: string): JournalWanderSession {
  const shuffled = shuffleJournalNames(names);
  return { names: shuffled, history: shuffled.slice(0, 1), index: 0, date, timezone };
}

import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRequestToken, memoServiceClient } from "@/connect";
import useCurrentUser from "@/hooks/useCurrentUser";
import { memoDetailQueryOptions, memoKeys } from "@/hooks/useMemoQueries";
import { isJournalCandidate } from "@/lib/journal";
import { buildMemoCreatorFilter } from "@/lib/resource-names";
import { State } from "@/types/proto/api/v1/common_pb";
import type { Memo } from "@/types/proto/api/v1/memo_service_pb";

export interface JournalPreferences {
  excludedMemoNames: string[];
  calendarVisible: boolean;
  calendarColor: boolean;
  timezone: string;
}

export const journalKeys = {
  all: ["journal"] as const,
  preferences: (owner: string) => ["journal", owner, "preferences"] as const,
  review: (owner: string, date: string, timezone: string) => ["journal", owner, "review", date, timezone] as const,
  calendar: (owner: string, month: string, timezone: string) => ["journal", owner, "calendar", month, timezone] as const,
};

/** Journal extensions share the existing session and never place credentials in a URL. */
export async function journalRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = await getRequestToken();
  const headers = new Headers(options.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (options.body) headers.set("Content-Type", "application/json");
  const response = await fetch(`/api/v1/journal${path}`, { ...options, headers, credentials: "same-origin" });
  if (!response.ok) {
    const body = await response.json().catch(() => undefined);
    throw new Error(typeof body?.message === "string" ? body.message : "暂时无法读取，请重试。");
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

export function useJournalPreferences() {
  const user = useCurrentUser();
  return useQuery({
    queryKey: journalKeys.preferences(user?.name ?? ""),
    queryFn: ({ signal }) => journalRequest<JournalPreferences>("/preferences", { signal }),
    enabled: Boolean(user),
    staleTime: 30_000,
  });
}

export function useUpdateJournalPreferences() {
  const user = useCurrentUser();
  const client = useQueryClient();
  return useMutation({
    mutationFn: (patch: Partial<JournalPreferences>) =>
      journalRequest<JournalPreferences>("/preferences", {
        method: "PATCH",
        body: JSON.stringify(patch),
      }),
    onSuccess: (preferences) => {
      client.setQueryData(journalKeys.preferences(user?.name ?? ""), preferences);
      void client.invalidateQueries({ queryKey: journalKeys.all });
    },
  });
}

export function useJournalReview(date: string, timezone: string) {
  const user = useCurrentUser();
  return useQuery({
    queryKey: journalKeys.review(user?.name ?? "", date, timezone),
    queryFn: ({ signal }) =>
      journalRequest<{ memoNames: string[]; date: string }>(`/review?${new URLSearchParams({ date, timezone })}`, { signal }),
    enabled: Boolean(user),
    staleTime: 0,
  });
}

export function useJournalCalendar(month: string, timezone: string, enabled = true) {
  const user = useCurrentUser();
  return useQuery({
    queryKey: [...memoKeys.lists(), "journal-calendar", user?.name, month, timezone],
    queryFn: ({ signal }) =>
      journalRequest<{ counts: Record<string, number> }>(`/calendar?${new URLSearchParams({ month, timezone })}`, { signal }),
    enabled: Boolean(user) && enabled,
    staleTime: 15_000,
  });
}

export function useJournalMemoDetails(names: readonly string[]) {
  return useQueries({ queries: names.map((name) => ({ ...memoDetailQueryOptions(name), staleTime: 0 })) });
}

/** Walk the entire real collection before sampling; a failed page never masquerades as a complete pool. */
export function useJournalCandidates(date: string, timezone: string, excludedMemoNames: readonly string[]) {
  const user = useCurrentUser();
  return useQuery({
    queryKey: [...memoKeys.lists(), "journal-candidates", user?.name, date, timezone, [...excludedMemoNames].sort()],
    queryFn: async ({ signal }) => {
      const result: Memo[] = [];
      let pageToken = "";
      const seenTokens = new Set<string>();
      const excluded = new Set(excludedMemoNames);
      do {
        const page = await memoServiceClient.listMemos(
          {
            pageSize: 100,
            pageToken,
            state: State.NORMAL,
            orderBy: "create_time desc",
            filter: buildMemoCreatorFilter(user?.name ?? ""),
          },
          { signal },
        );
        result.push(...page.memos.filter((memo) => isJournalCandidate(memo, user?.name ?? "", date, timezone, excluded)));
        pageToken = page.nextPageToken;
        if (pageToken && seenTokens.has(pageToken)) throw new Error("记录分页未能完成，请重试。");
        seenTokens.add(pageToken);
      } while (pageToken);
      return [...new Map(result.map((memo) => [memo.name, memo])).values()];
    },
    enabled: Boolean(user),
    staleTime: 0,
  });
}

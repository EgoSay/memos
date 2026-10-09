import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import useCurrentUser from "@/hooks/useCurrentUser";
import { journalRequest } from "@/hooks/useJournalQueries";

export interface JournalPartition {
  id: string;
  name: string;
  version: number;
}
export interface JournalTarget {
  id: string;
  partitionId: string;
  name: string;
  url: string;
  enabled: boolean;
  autoUpdate: boolean;
  retract: boolean;
  fields: { content: boolean; date: boolean; media: boolean };
  version: number;
  hasSigningSecret: boolean;
  signingSecret?: string;
}
export interface JournalDelivery {
  id: string;
  memoUid: string;
  targetId: string;
  targetName: string;
  event: "upsert" | "retract";
  status: string;
  attempts: number;
  createdTs: number;
  lastError?: string;
  publishedUrl?: string;
}
export interface MemoPartition {
  memoUid: string;
  partitionId: string;
  version: number;
  suspended?: boolean;
}
export const partitionKeys = ["journal-partitions"] as const;
const encode = encodeURIComponent;

export function useJournalPartitions() {
  const owner = useCurrentUser()?.name;
  return useQuery({
    queryKey: [...partitionKeys, owner],
    enabled: Boolean(owner),
    queryFn: ({ signal }) => journalRequest<{ partitions: JournalPartition[] }>("/partitions", { signal }),
  });
}
export function useJournalPartitionTargets(partitionId: string) {
  const owner = useCurrentUser()?.name;
  return useQuery({
    queryKey: [...partitionKeys, owner, partitionId, "targets"],
    enabled: Boolean(owner && partitionId),
    queryFn: ({ signal }) => journalRequest<{ targets: JournalTarget[] }>(`/partitions/${encode(partitionId)}/targets`, { signal }),
  });
}
export function useJournalDeliveries() {
  const owner = useCurrentUser()?.name;
  return useQuery({
    queryKey: [...partitionKeys, owner, "deliveries"],
    enabled: Boolean(owner),
    queryFn: ({ signal }) => journalRequest<{ deliveries: JournalDelivery[] }>("/deliveries", { signal }),
    refetchInterval: 10_000,
  });
}
export function useJournalPartitionAction() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ path, method = "POST", body }: { path: string; method?: string; body?: unknown }) =>
      journalRequest<JournalTarget>(path, { method, ...(body === undefined ? {} : { body: JSON.stringify(body) }) }),
    onSuccess: () => client.invalidateQueries({ queryKey: partitionKeys }),
  });
}
export async function getMemoPartition(memoUid: string) {
  return journalRequest<MemoPartition>(`/memos/${encode(memoUid.replace(/^memos\//, ""))}/partition`);
}
/** Call only after the complete memo and every attachment are durably saved. */
export async function finishPartitionSave(memoUid: string, partitionId: string, explicitSelection = false): Promise<void> {
  const uid = encode(memoUid.replace(/^memos\//, ""));
  const mapping = await getMemoPartition(memoUid);
  if (mapping.suspended && !explicitSelection) return;
  if (mapping.partitionId !== partitionId || mapping.suspended) {
    await journalRequest(`/memos/${uid}/partition`, { method: "PUT", body: JSON.stringify({ partitionId, version: mapping.version }) });
  }
  if (partitionId) await journalRequest(`/memos/${uid}/send`, { method: "POST", body: JSON.stringify({ manual: false }) });
}

export function useJournalPartitionMemos(partitionId: string, enabled: boolean) {
  const owner = useCurrentUser()?.name;
  return useQuery({
    queryKey: [...partitionKeys, owner, partitionId, "memos"],
    enabled: Boolean(owner && partitionId && enabled),
    queryFn: ({ signal }) =>
      journalRequest<{ memos: { uid: string; content: string; recordTime: number }[] }>(`/partitions/${encode(partitionId)}/memos`, {
        signal,
      }),
  });
}

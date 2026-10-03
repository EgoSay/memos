import { useEffect } from "react";
import { useJournalPartitions, useJournalPartitionTargets } from "@/hooks/useJournalPartitionQueries";

interface Props {
  value: string;
  onChange: (value: string) => void;
  onSyncChange?: (enabled: boolean) => void;
  disabled?: boolean;
  suspended?: boolean;
}
export default function PartitionPicker({ value, onChange, onSyncChange, disabled, suspended }: Props) {
  const partitions = useJournalPartitions();
  const targets = useJournalPartitionTargets(value);
  const active = targets.data?.targets.filter((target) => target.enabled) ?? [];
  const hasSync = active.length > 0 && !suspended;
  useEffect(() => {
    onSyncChange?.(hasSync);
  }, [hasSync, onSyncChange]);
  if (!partitions.data?.partitions.length && !value) return null;
  return (
    <div className="space-y-1.5 text-sm">
      <label className="inline-flex min-h-11 items-center gap-2 text-muted-foreground">
        <span>分区</span>
        <select
          aria-label="记录所属分区"
          className="min-h-11 rounded-md border border-border bg-background px-2 text-foreground"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          disabled={disabled || partitions.isPending}
        >
          <option value="">未分区</option>
          {partitions.data?.partitions.map((partition) => (
            <option key={partition.id} value={partition.id}>
              {partition.name}
            </option>
          ))}
        </select>
      </label>
      {suspended && <p className="text-xs leading-5 text-muted-foreground">同步已暂停，重新选择分区后启用。</p>}
      {hasSync && (
        <p className="text-xs leading-5 text-muted-foreground">完整保存后自动同步到：{active.map((target) => target.name).join("、")}</p>
      )}
      {targets.isError && value && (
        <p role="alert" className="text-xs text-destructive">
          未能读取此分区的同步设置，请重试后保存。
        </p>
      )}
    </div>
  );
}

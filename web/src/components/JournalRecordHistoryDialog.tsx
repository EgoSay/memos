import { fromJson, type JsonValue } from "@bufbuild/protobuf";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { journalRequest } from "@/hooks/useJournalQueries";
import { MemoSchema } from "@/types/proto/api/v1/memo_service_pb";

export default function JournalRecordHistoryDialog({
  name,
  open,
  onOpenChange,
}: {
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const uid = encodeURIComponent(name.replace(/^memos\//, ""));
  const client = useQueryClient();
  const versions = useQuery({
    queryKey: ["journal", "revisions", name],
    enabled: open,
    queryFn: () =>
      journalRequest<{
        revisions: { id: string; createdTs: number; memo: JsonValue }[];
        provenance?: { source?: string; modifiedTs?: number; enteredTs?: number; importedTs?: number; imported?: boolean };
      }>(`/memos/${uid}/revisions`),
  });
  const restore = useMutation({
    mutationFn: (id: string) => journalRequest(`/memos/${uid}/revisions/${encodeURIComponent(id)}/restore`, { method: "POST" }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["memos"] });
      await versions.refetch();
      onOpenChange(false);
    },
  });
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent size="xl">
        <DialogHeader>
          <DialogTitle>最近版本</DialogTitle>
          <DialogDescription>保留最近 30 天。恢复后仍可找回当前版本。</DialogDescription>
        </DialogHeader>
        <div className="overflow-y-auto space-y-3">
          {versions.data && (
            <p className="text-sm text-muted-foreground">
              来源：
              {versions.data.provenance?.imported
                ? "导入"
                : versions.data.provenance?.source === "manual"
                  ? "手动记录"
                  : "已有记录（未留存来源）"}
              {versions.data.provenance?.enteredTs
                ? ` · 实际录入于 ${new Date(versions.data.provenance.enteredTs * 1000).toLocaleString()}`
                : versions.data.provenance?.importedTs
                  ? ` · 导入于 ${new Date(versions.data.provenance.importedTs * 1000).toLocaleString()}`
                  : ""}
              {versions.data.provenance?.modifiedTs
                ? ` · 实际修改于 ${new Date(versions.data.provenance.modifiedTs * 1000).toLocaleString()}`
                : ""}
            </p>
          )}
          {versions.isPending && <p role="status">正在读取…</p>}
          {versions.isError && <p role="alert">无法读取版本，请重试。</p>}
          {!versions.isPending && !versions.data?.revisions.length && (
            <p className="text-sm text-muted-foreground">这条记录还没有最近的修改版本。</p>
          )}
          {versions.data?.revisions.map((version) => {
            const memo = fromJson(MemoSchema, version.memo);
            return (
              <details key={version.id} className="rounded border p-3">
                <summary className="cursor-pointer py-2">
                  {new Date(version.createdTs * 1000).toLocaleString()} · {memo.attachments.length} 个附件
                </summary>
                <p className="whitespace-pre-wrap break-words py-3 leading-7">{memo.content || "媒体记录"}</p>
                <Button variant="outline" disabled={restore.isPending} onClick={() => restore.mutate(version.id)}>
                  恢复这个版本
                </Button>
              </details>
            );
          })}
          {restore.isError && (
            <p role="alert" className="text-sm text-destructive">
              {restore.error.message}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

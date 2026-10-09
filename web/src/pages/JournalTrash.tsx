import { fromJson, type JsonValue } from "@bufbuild/protobuf";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import useCurrentUser from "@/hooks/useCurrentUser";
import { journalRequest } from "@/hooks/useJournalQueries";
import { MemoSchema } from "@/types/proto/api/v1/memo_service_pb";

export default function JournalTrash() {
  const owner = useCurrentUser();
  const client = useQueryClient();
  const [purge, setPurge] = useState<string>();
  const records = useQuery({
    queryKey: ["journal", owner?.name, "trash"],
    queryFn: () => journalRequest<{ memos: JsonValue[] }>("/trash"),
  });
  const action = useMutation({
    mutationFn: ({ name, permanent }: { name: string; permanent?: boolean }) =>
      journalRequest(`/trash/${encodeURIComponent(name.replace(/^memos\//, ""))}${permanent ? "" : "/restore"}`, {
        method: permanent ? "DELETE" : "POST",
      }),
    onSuccess: async () => {
      setPurge(undefined);
      await client.invalidateQueries({ queryKey: ["journal"] });
      await client.invalidateQueries({ queryKey: ["memos"] });
    },
  });
  return (
    <main className="mx-auto w-full max-w-3xl space-y-6 px-4 py-8 sm:px-8">
      <Link to="/" className="text-sm text-muted-foreground underline">
        回到记录
      </Link>
      <header>
        <h1 className="text-2xl">最近删除</h1>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">在这里保留 30 天。恢复后记录只对你可见，旧分享和同步不会恢复。</p>
      </header>
      {records.isPending && <p role="status">正在读取…</p>}
      {records.isError && <p role="alert">{records.error.message}</p>}
      {!records.isPending && !records.data?.memos.length && <p className="text-muted-foreground">这里暂时没有记录。</p>}
      {records.data?.memos.map((raw) => {
        const memo = fromJson(MemoSchema, raw);
        return (
          <article className="space-y-3 rounded-xl border p-5" key={memo.name}>
            <p className="whitespace-pre-wrap break-words leading-7">{memo.content || "媒体记录"}</p>
            {memo.attachments.length > 0 && <p className="text-sm text-muted-foreground">{memo.attachments.length} 个附件会随记录恢复</p>}
            <div className="flex gap-3">
              <Button variant="outline" disabled={action.isPending} onClick={() => action.mutate({ name: memo.name })}>
                恢复
              </Button>
              <Button variant="ghost" disabled={action.isPending} onClick={() => setPurge(memo.name)}>
                彻底删除
              </Button>
            </div>
          </article>
        );
      })}
      {action.isError && (
        <p role="alert" className="text-destructive">
          {action.error.message}
        </p>
      )}
      <Dialog
        open={Boolean(purge)}
        onOpenChange={(open) => {
          if (!open) setPurge(undefined);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>彻底删除这条记录？</DialogTitle>
            <DialogDescription>
              正文、附件原件和最近版本将从当前资料库移除，无法在此恢复。独立备份、已下载或已外发的副本不受影响。
            </DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setPurge(undefined)}>
              保留
            </Button>
            <Button
              variant="destructive"
              disabled={action.isPending}
              onClick={() => purge && action.mutate({ name: purge, permanent: true })}
            >
              彻底删除
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </main>
  );
}

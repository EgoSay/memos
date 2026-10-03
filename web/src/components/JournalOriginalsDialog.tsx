import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { getRequestToken, memoServiceClient } from "@/connect";
import { journalRequest } from "@/hooks/useJournalQueries";

export default function JournalOriginalsDialog({
  name,
  open,
  onOpenChange,
}: {
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [error, setError] = useState("");
  const originals = useQuery({
    queryKey: ["journal", "originals", name],
    enabled: open,
    queryFn: async () => {
      const memo = await memoServiceClient.getMemo({ name });
      return Promise.all(
        memo.attachments.map(async (attachment) => ({
          attachment,
          info: await journalRequest<{ available: boolean; sha256?: string; size?: number }>(`/${attachment.name}/original-info`),
        })),
      );
    },
  });
  const download = async (resource: string, filename: string) => {
    try {
      const token = await getRequestToken();
      const response = await fetch(`/api/v1/journal/${resource}/original`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        credentials: "same-origin",
      });
      if (!response.ok) throw new Error("原文件暂时无法读取，请重试。");
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "下载未完成");
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent size="xl">
        <DialogHeader>
          <DialogTitle>原文件</DialogTitle>
          <DialogDescription>只有你可以下载原始字节；分享使用独立的显示副本。旧记录可能没有保留原文件。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 overflow-y-auto">
          {originals.isPending && <p role="status">正在读取…</p>}
          {(originals.isError || error) && (
            <p role="alert" className="text-sm text-destructive">
              {error || originals.error?.message}
            </p>
          )}
          {originals.data?.length === 0 && <p>这条记录没有附件。</p>}
          {originals.data?.map(({ attachment, info }) => (
            <div key={attachment.name} className="rounded border p-3 space-y-2">
              <p className="break-all">{attachment.filename}</p>
              {info.available ? (
                <>
                  <p className="text-xs text-muted-foreground break-all">SHA-256：{info.sha256}</p>
                  <Button variant="outline" onClick={() => void download(attachment.name, attachment.filename)}>
                    下载原文件
                  </Button>
                </>
              ) : (
                <p className="text-sm text-muted-foreground">未保留原文件，已有显示版本仍可使用。</p>
              )}
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}

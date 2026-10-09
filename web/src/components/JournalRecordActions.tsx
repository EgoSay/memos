import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "react-hot-toast";
import JournalOriginalsDialog from "@/components/JournalOriginalsDialog";
import JournalRecordHistoryDialog from "@/components/JournalRecordHistoryDialog";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { journalRequest } from "@/hooks/useJournalQueries";

export default function JournalRecordActions({ name, includeDelete = true }: { name: string; includeDelete?: boolean }) {
  const [historyOpen, setHistoryOpen] = useState(false);
  const [originalsOpen, setOriginalsOpen] = useState(false);
  const client = useQueryClient();
  const uid = encodeURIComponent(name.replace(/^memos\//, ""));
  const remove = useMutation({
    mutationFn: () => journalRequest(`/memos/${uid}/trash`, { method: "POST" }),
    onSuccess: async () => {
      client.removeQueries({ queryKey: ["memos", "detail", name] });
      await client.invalidateQueries({ queryKey: ["memos"] });
      await client.invalidateQueries({ queryKey: ["journal"] });
      toast.success("已移到最近删除，30 天内可以恢复。");
    },
    onError: (error) => toast.error(error.message),
  });
  return (
    <>
      <DropdownMenuItem
        className="min-h-11"
        onClick={(event) => {
          event.preventDefault();
          setHistoryOpen(true);
        }}
      >
        最近版本
      </DropdownMenuItem>
      {includeDelete && (
        <DropdownMenuItem className="min-h-11" disabled={remove.isPending} onClick={() => remove.mutate()}>
          移到最近删除
        </DropdownMenuItem>
      )}
      <DropdownMenuItem
        className="min-h-11"
        onClick={(event) => {
          event.preventDefault();
          setOriginalsOpen(true);
        }}
      >
        原文件
      </DropdownMenuItem>
      <JournalOriginalsDialog name={name} open={originalsOpen} onOpenChange={setOriginalsOpen} />
      <JournalRecordHistoryDialog name={name} open={historyOpen} onOpenChange={setHistoryOpen} />
    </>
  );
}

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import type { Memo } from "@/types/proto/api/v1/memo_service_pb";
import { useEditorContext, useEditorSelector } from "../state";

/** Resolving a conflict changes the edit baseline only after an explicit choice. */
export function RecordConflict({ latest, onClose }: { latest: Memo; onClose: () => void }) {
  const { getState, dispatch, actions } = useEditorContext();
  const content = useEditorSelector((state) => state.content);
  const files = useEditorSelector((state) => state.localFiles.length + state.metadata.attachments.length);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent size="xl">
        <DialogHeader>
          <DialogTitle>这条记录还有另一份修改</DialogTitle>
          <DialogDescription>两份内容都保留着。比较后可回到编辑框手动合并；再次保存前还会检查其他设备是否又有修改。</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2 overflow-y-auto">
          <section className="space-y-2">
            <h3 className="text-sm font-medium">当前编辑内容 · {files} 个附件</h3>
            <p className="whitespace-pre-wrap break-words rounded border p-3 text-sm">{content || "媒体记录"}</p>
          </section>
          <section className="space-y-2">
            <h3 className="text-sm font-medium">服务端最新内容 · {latest.attachments.length} 个附件</h3>
            <p className="whitespace-pre-wrap break-words rounded border p-3 text-sm">{latest.content || "媒体记录"}</p>
            <ul className="text-xs text-muted-foreground">
              {latest.attachments.map((file) => (
                <li key={file.name}>{file.filename}</li>
              ))}
            </ul>
          </section>
        </div>
        <p className="text-sm text-muted-foreground">
          选择继续编辑不会立刻覆盖记录。下一次点击保存时，将以编辑框中的正文和附件更新记录，之前的版本可在最近版本中恢复。
        </p>
        <div className="flex flex-wrap gap-2 justify-end">
          <Button variant="outline" onClick={onClose}>
            暂不处理
          </Button>
          <Button
            onClick={() => {
              dispatch(actions.restoreDraft({ ...getState(), baselineMemo: latest }));
              onClose();
            }}
          >
            已比较，继续编辑
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

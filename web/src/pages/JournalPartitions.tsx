import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  type JournalPartition,
  type JournalTarget,
  useJournalDeliveries,
  useJournalPartitionAction,
  useJournalPartitionMemos,
  useJournalPartitions,
  useJournalPartitionTargets,
} from "@/hooks/useJournalPartitionQueries";

const statusText: Record<string, string> = {
  pending: "等待发送",
  delivering: "正在发送",
  delivered: "Webhook 已送达 · 发布结果未知",
  published: "接收端回执：已发布",
  failed: "发送失败",
  cancelled: "已停止发送",
  local_changed: "本地已更新 · 尚未发送",
};
const targetBody = (target: JournalTarget) => ({
  partitionId: target.partitionId,
  name: target.name,
  url: target.url,
  enabled: target.enabled,
  autoUpdate: target.autoUpdate,
  retract: target.retract,
  fields: target.fields,
  version: target.version,
  signingSecret: "",
});

function TargetForm({ partitionId, existing, onClose }: { partitionId: string; existing?: JournalTarget; onClose: () => void }) {
  const action = useJournalPartitionAction();
  const [name, setName] = useState(existing?.name ?? "");
  const [url, setUrl] = useState(existing?.url ?? "");
  const [secret, setSecret] = useState("");
  const [enabled, setEnabled] = useState(existing?.enabled ?? false);
  const [autoUpdate, setAutoUpdate] = useState(existing?.autoUpdate ?? false);
  const [retract, setRetract] = useState(existing?.retract ?? false);
  const [fields, setFields] = useState(existing?.fields ?? { content: true, date: true, media: false });
  const [createdSecret, setCreatedSecret] = useState("");
  const [removeArmed, setRemoveArmed] = useState(false);
  if (createdSecret)
    return (
      <section className="space-y-4 rounded-lg border border-border p-4">
        <p>目标已保存。请将签名密钥填入接收端；关闭后不会再次显示。</p>
        <Input aria-label="新目标签名密钥" readOnly value={createdSecret} onFocus={(event) => event.target.select()} />
        <Button className="min-h-11" onClick={onClose}>
          已保存密钥
        </Button>
      </section>
    );
  return (
    <form
      className="space-y-4 rounded-lg border border-border bg-background p-4"
      onSubmit={async (event) => {
        event.preventDefault();
        try {
          const value = await action.mutateAsync({
            path: existing ? `/targets/${existing.id}` : `/partitions/${partitionId}/targets`,
            method: existing ? "PUT" : "POST",
            body: { partitionId, name, url, signingSecret: secret, enabled, autoUpdate, retract, fields, version: existing?.version ?? 0 },
          });
          if (value?.signingSecret) setCreatedSecret(value.signingSecret);
          else onClose();
        } catch {
          /* The mutation error remains beside this form. */
        }
      }}
    >
      <label className="block space-y-2">
        <span className="text-sm">目标名称</span>
        <Input required value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：我的博客" className="min-h-11" />
      </label>
      <label className="block space-y-2">
        <span className="text-sm">Webhook 地址</span>
        <Input
          type="url"
          required
          value={url}
          onChange={(event) => setUrl(event.target.value)}
          placeholder="https://…"
          className="min-h-11"
        />
      </label>
      <label className="block space-y-2">
        <span className="text-sm">签名密钥（可留空）</span>
        <Input
          type="password"
          autoComplete="new-password"
          value={secret}
          onChange={(event) => setSecret(event.target.value)}
          placeholder={existing ? "留空保留现有密钥" : "留空生成一个新密钥"}
          className="min-h-11"
        />
      </label>
      <fieldset className="space-y-1">
        <legend className="mb-1 text-sm">允许发送的内容</legend>
        {(
          [
            ["content", "正文"],
            ["date", "记录日期"],
            ["media", "图片、录音与视频"],
          ] as const
        ).map(([key, label]) => (
          <label key={key} className="flex min-h-11 items-center gap-3 text-sm">
            <input type="checkbox" checked={fields[key]} onChange={(event) => setFields({ ...fields, [key]: event.target.checked })} />
            {label}
          </label>
        ))}
        <p className="text-xs leading-5 text-muted-foreground">
          图片去除定位等 EXIF 信息后发送。正文与媒体合计上限为 64 MiB，超限时会保留记录并显示投递失败。
        </p>
      </fieldset>
      <div className="border-t border-border pt-3">
        <label className="flex min-h-11 items-center gap-3 text-sm">
          <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
          启用未来记录的自动同步
        </label>
        <p className="text-xs leading-5 text-muted-foreground">
          启用后，此分区未来完整保存的记录会自动发送到上述地址。现有记录不会补发；暂停后恢复也只对未来保存生效。
        </p>
        <label className="flex min-h-11 items-center gap-3 text-sm">
          <input type="checkbox" checked={autoUpdate} onChange={(event) => setAutoUpdate(event.target.checked)} />
          同时自动发送后续修改
        </label>
        <label className="flex min-h-11 items-center gap-3 text-sm">
          <input type="checkbox" checked={retract} onChange={(event) => setRetract(event.target.checked)} />
          删除、归档或移出分区时请求撤回已发送副本
        </label>
        <p className="text-xs leading-5 text-muted-foreground">更新和撤回需要接收端支持同一记录的映射。没有回执时只显示 Webhook 已送达。</p>
      </div>
      {existing && (
        <div className="space-y-2 border-t border-border pt-2">
          <Button type="button" variant="ghost" className="min-h-11" onClick={() => setRemoveArmed(!removeArmed)}>
            移除这个目标
          </Button>
          {removeArmed && (
            <div>
              <p className="text-xs leading-5 text-muted-foreground">待发送任务会停止，已经送达的外部副本会保留。</p>
              <Button
                type="button"
                variant="outline"
                className="mt-2 min-h-11"
                disabled={action.isPending}
                onClick={async () => {
                  try {
                    await action.mutateAsync({ path: `/targets/${existing.id}?version=${existing.version}`, method: "DELETE" });
                    onClose();
                  } catch {
                    /* Error shown below. */
                  }
                }}
              >
                停止发送并移除
              </Button>
            </div>
          )}
        </div>
      )}
      {action.isError && (
        <p role="alert" className="text-sm text-destructive">
          {action.error.message}
        </p>
      )}
      <div className="flex gap-2">
        <Button className="min-h-11" type="submit" disabled={action.isPending}>
          {action.isPending ? "正在保存…" : "保存目标"}
        </Button>
        <Button className="min-h-11" type="button" variant="ghost" onClick={onClose}>
          取消
        </Button>
      </div>
    </form>
  );
}

function HistoricalSend({ partitionId }: { partitionId: string }) {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [sent, setSent] = useState(false);
  const records = useJournalPartitionMemos(partitionId, open);
  const action = useJournalPartitionAction();
  return (
    <details onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary className="min-h-11 cursor-pointer py-3 text-sm text-muted-foreground">选择历史记录补发</summary>
      <p className="mb-3 text-xs leading-5 text-muted-foreground">
        仅发送这里勾选的完整记录到本分区当前启用的目标。已经送达的相同版本不会重复创建。
      </p>
      {records.isPending && open && (
        <p role="status" className="text-sm">
          正在读取…
        </p>
      )}
      {records.isError && (
        <Button variant="outline" className="min-h-11" onClick={() => records.refetch()}>
          重试
        </Button>
      )}
      <div className="max-h-64 overflow-auto">
        {records.data?.memos.map((memo) => (
          <label key={memo.uid} className="flex min-h-11 items-start gap-3 border-b border-border/50 py-3 text-sm">
            <input
              className="mt-1"
              type="checkbox"
              checked={selected.includes(memo.uid)}
              disabled={!selected.includes(memo.uid) && selected.length >= 100}
              onChange={(event) => {
                setSent(false);
                setSelected(event.target.checked ? [...selected, memo.uid] : selected.filter((uid) => uid !== memo.uid));
              }}
            />
            <span>
              <span className="block text-xs text-muted-foreground">{new Date(memo.recordTime * 1000).toLocaleDateString()}</span>
              <span className="mt-1 block whitespace-pre-wrap">{memo.content || "图片或录音记录"}</span>
            </span>
          </label>
        ))}
      </div>
      {records.data?.memos.length === 0 && <p className="py-3 text-sm text-muted-foreground">这个分区还没有可发送的历史记录。</p>}
      {selected.length > 0 && (
        <Button
          className="mt-3 min-h-11"
          disabled={action.isPending}
          onClick={async () => {
            try {
              await action.mutateAsync({ path: `/partitions/${partitionId}/send`, body: { memoUids: selected } });
              setSelected([]);
              setSent(true);
            } catch {
              /* Error shown below. */
            }
          }}
        >
          发送选中的 {selected.length} 条记录
        </Button>
      )}
      {sent && (
        <p role="status" className="mt-3 text-sm text-muted-foreground">
          已处理所选记录，可在最近发送情况查看结果。
        </p>
      )}
      {action.isError && (
        <p role="alert" className="mt-3 text-sm text-destructive">
          {action.error.message}
        </p>
      )}
    </details>
  );
}

function PartitionSection({ partition }: { partition: JournalPartition }) {
  const targets = useJournalPartitionTargets(partition.id);
  const action = useJournalPartitionAction();
  const [editing, setEditing] = useState<JournalTarget | "new" | null>(null);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(partition.name);
  const [deleteArmed, setDeleteArmed] = useState(false);
  const [message, setMessage] = useState("");
  const run = async (input: Parameters<typeof action.mutateAsync>[0], done?: () => void) => {
    setMessage("");
    try {
      await action.mutateAsync(input);
      done?.();
    } catch {
      /* Rendered below. */
    }
  };
  return (
    <section className="space-y-5 rounded-xl border border-border/70 bg-card p-5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-medium">{partition.name}</h2>
        <div className="flex gap-1">
          <Button className="min-h-11" variant="ghost" onClick={() => setRenaming(!renaming)}>
            改名
          </Button>
          <Button className="min-h-11" variant="ghost" onClick={() => setDeleteArmed(!deleteArmed)}>
            删除分区
          </Button>
        </div>
      </div>
      {renaming && (
        <form
          className="flex gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            void run({ path: `/partitions/${partition.id}`, method: "PUT", body: { name, version: partition.version } }, () =>
              setRenaming(false),
            );
          }}
        >
          <Input aria-label="新的分区名称" className="min-h-11" required value={name} onChange={(event) => setName(event.target.value)} />
          <Button className="min-h-11" type="submit" disabled={action.isPending}>
            保存
          </Button>
        </form>
      )}
      {deleteArmed && (
        <div className="space-y-2 rounded-md bg-muted/50 p-3">
          <p className="text-sm leading-6">记录与附件会保留在全部记录中，分区的同步目标和待发送任务会停止。</p>
          <Button
            variant="outline"
            className="min-h-11"
            disabled={action.isPending}
            onClick={() => void run({ path: `/partitions/${partition.id}?version=${partition.version}`, method: "DELETE" })}
          >
            保留记录并删除分区
          </Button>
        </div>
      )}
      <div className="space-y-3">
        {targets.isPending && (
          <p role="status" className="text-sm text-muted-foreground">
            正在读取同步目标…
          </p>
        )}
        {targets.isError && (
          <Button className="min-h-11" variant="outline" onClick={() => targets.refetch()}>
            重试读取目标
          </Button>
        )}
        {targets.data?.targets.length === 0 && <p className="text-sm text-muted-foreground">此分区保持私密，还没有设置同步目标。</p>}
        {targets.data?.targets.map((target) => (
          <div key={target.id} className="rounded-lg border border-border/60 p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p className="text-sm font-medium">{target.name}</p>
                <p className="mt-1 text-xs text-muted-foreground">{target.enabled ? "未来完整保存的记录会自动发送" : "同步未启用"}</p>
              </div>
              <div className="flex flex-wrap gap-1">
                <Button className="min-h-11" variant="ghost" onClick={() => setEditing(target)}>
                  设置
                </Button>
                {target.enabled && (
                  <Button
                    className="min-h-11"
                    variant="outline"
                    disabled={action.isPending}
                    onClick={() =>
                      void run({ path: `/targets/${target.id}`, method: "PUT", body: { ...targetBody(target), enabled: false } })
                    }
                  >
                    暂停
                  </Button>
                )}
                <Button
                  className="min-h-11"
                  variant="ghost"
                  disabled={action.isPending}
                  onClick={() =>
                    void run({ path: `/targets/${target.id}/test` }, () => setMessage("测试已送达；测试内容没有使用私人记录。"))
                  }
                >
                  发送示例测试
                </Button>
              </div>
            </div>
          </div>
        ))}
      </div>
      {editing ? (
        <TargetForm
          key={editing === "new" ? "new" : editing.id}
          partitionId={partition.id}
          existing={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      ) : (
        <Button className="min-h-11" variant="outline" onClick={() => setEditing("new")}>
          添加同步目标
        </Button>
      )}
      {targets.data?.targets.some((target) => target.enabled) && <HistoricalSend partitionId={partition.id} />}
      {message && (
        <p role="status" className="text-sm text-muted-foreground">
          {message}
        </p>
      )}
      {action.isError && (
        <p role="alert" className="text-sm text-destructive">
          {action.error.message}
        </p>
      )}
    </section>
  );
}

export default function JournalPartitions() {
  const partitions = useJournalPartitions();
  const deliveries = useJournalDeliveries();
  const action = useJournalPartitionAction();
  const [name, setName] = useState("");
  return (
    <div className="mx-auto w-full max-w-3xl space-y-7 pb-12 pt-5 sm:pt-10">
      <header>
        <h1 className="font-serif text-3xl font-medium tracking-tight">分区与同步</h1>
        <p className="mt-3 text-sm leading-6 text-muted-foreground">想分类的时候，再放进一个分区。所有记录仍留在一起。</p>
      </header>
      <form
        className="flex gap-2"
        onSubmit={async (event) => {
          event.preventDefault();
          try {
            await action.mutateAsync({ path: "/partitions", body: { name } });
            setName("");
          } catch {
            /* Rendered below. */
          }
        }}
      >
        <Input
          aria-label="新分区名称"
          placeholder="新分区名称"
          className="min-h-11"
          value={name}
          onChange={(event) => setName(event.target.value)}
          required
        />
        <Button className="min-h-11" type="submit" disabled={action.isPending}>
          创建分区
        </Button>
      </form>
      {action.isError && (
        <p role="alert" className="text-sm text-destructive">
          {action.error.message}
        </p>
      )}
      {partitions.isPending && (
        <p role="status" className="text-sm text-muted-foreground">
          正在读取分区…
        </p>
      )}
      {partitions.isError && (
        <Button variant="outline" className="min-h-11" onClick={() => partitions.refetch()}>
          重试读取分区
        </Button>
      )}
      {partitions.data?.partitions.map((partition) => (
        <PartitionSection key={partition.id} partition={partition} />
      ))}
      <details className="border-t border-border pt-3">
        <summary className="min-h-11 cursor-pointer py-3 text-sm">最近发送情况</summary>
        <p className="mb-4 text-xs leading-5 text-muted-foreground">
          只显示你授权的目标。Webhook 已送达表示接收成功，社媒发布需要接收端返回对应回执。
        </p>
        {deliveries.isError && (
          <Button className="min-h-11" variant="outline" onClick={() => deliveries.refetch()}>
            重试读取发送情况
          </Button>
        )}
        {deliveries.data?.deliveries.length === 0 && <p className="text-sm text-muted-foreground">还没有发送记录。</p>}
        <div className="divide-y divide-border">
          {deliveries.data?.deliveries.map((delivery) => (
            <div key={delivery.id} className="space-y-2 py-4 text-sm">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p>
                  {delivery.targetName} · {delivery.event === "retract" ? "撤回请求" : "记录同步"}
                </p>
                <time className="text-xs text-muted-foreground">{new Date(delivery.createdTs * 1000).toLocaleString()}</time>
              </div>
              <p>{statusText[delivery.status] ?? delivery.status}</p>
              {delivery.lastError && <p className="text-xs leading-5 text-muted-foreground">{delivery.lastError}</p>}
              <div className="flex flex-wrap items-center gap-3">
                <Link className="inline-flex min-h-11 items-center underline underline-offset-4" to={`/memos/${delivery.memoUid}`}>
                  原记录
                </Link>
                {delivery.publishedUrl && (
                  <a
                    className="inline-flex min-h-11 items-center underline underline-offset-4"
                    href={delivery.publishedUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    打开发布回执
                  </a>
                )}
                {["failed", "local_changed"].includes(delivery.status) && (
                  <Button
                    className="min-h-11"
                    variant="outline"
                    disabled={action.isPending}
                    onClick={() => action.mutate({ path: `/deliveries/${delivery.id}/retry` })}
                  >
                    {delivery.status === "local_changed" ? "发送这次修改" : "重试"}
                  </Button>
                )}
              </div>
            </div>
          ))}
        </div>
      </details>
    </div>
  );
}

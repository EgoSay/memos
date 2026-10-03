import { State } from "@/types/proto/api/v1/common_pb";
import { type Memo, Visibility } from "@/types/proto/api/v1/memo_service_pb";

const encoder = new TextEncoder();
const floatBits = (value: number) => {
  const buffer = new ArrayBuffer(8);
  new DataView(buffer).setFloat64(0, value, false);
  return Array.from(new Uint8Array(buffer), (byte) => byte.toString(16).padStart(2, "0")).join("");
};

/** UTF-8 length-prefixed tuple, matching store.MemoRecordSnapshot.Hash exactly. */
export async function journalRecordVersion(memo: Memo): Promise<string> {
  const location = memo.location;
  const attachments = memo.attachments.map((item) => item.name.replace(/^attachments\//, "")).sort();
  const fields = [
    "journal-record-v1",
    memo.content,
    String(memo.createTime?.seconds ?? 0),
    String(memo.updateTime?.seconds ?? 0),
    Visibility[memo.visibility],
    (memo.space ?? "").replace(/^spaces\//, ""),
    memo.state === State.ARCHIVED ? "ARCHIVED" : "NORMAL",
    location ? "1" : "0",
    location?.placeholder ?? "",
    location ? floatBits(location.latitude) : "",
    location ? floatBits(location.longitude) : "",
    String(attachments.length),
    ...attachments,
  ];
  const tuple = fields.map((value) => `${encoder.encode(value).byteLength}:${value}`).join("");
  const digest = await crypto.subtle.digest("SHA-256", encoder.encode(tuple));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

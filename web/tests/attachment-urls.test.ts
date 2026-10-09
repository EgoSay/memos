import { describe, expect, it } from "vitest";
import type { Attachment } from "@/types/proto/api/v1/attachment_service_pb";
import { getAttachmentMotionClipUrl, getAttachmentThumbnailUrl, getAttachmentUrl } from "@/utils/attachment";

const origin = window.location.origin;

const baseAttachment = {
  name: "attachments/test-uid",
  filename: "photo.png",
  type: "image/png",
} as Attachment;

describe("attachment filenames as URL path segments", () => {
  it("encodes the actual Chinese filename without turning its # or ? into URL syntax", () => {
    const attachment = { ...baseAttachment, filename: "DEMO 中文 #1?.png" };
    const url = new URL(getAttachmentUrl(attachment));
    expect(url.pathname).toBe("/file/attachments/test-uid/DEMO%20%E4%B8%AD%E6%96%87%20%231%3F.png");
    expect(url.hash).toBe("");
    expect(url.search).toBe("");
  });

  it.each([
    "DEMO 中文 #1?.png",
    "100% + spring.jpg",
    "a/b\\c #?.heic",
    "literal%23name.png",
  ])("preserves the filename %s and real thumbnail/motion query parameters", (filename) => {
    const attachment = { ...baseAttachment, filename };
    for (const [build, selector] of [
      [getAttachmentUrl, undefined],
      [getAttachmentThumbnailUrl, "thumbnail"],
      [getAttachmentMotionClipUrl, "motion"],
    ] as const) {
      const url = new URL(build(attachment));
      const parts = url.pathname.split("/");
      expect(parts).toHaveLength(5);
      expect(decodeURIComponent(parts[4])).toBe(filename);
      expect(url.hash).toBe("");
      expect([...url.searchParams.entries()]).toEqual(selector ? [[selector, "true"]] : []);
    }
  });

  it("keeps external source links verbatim while using encoded managed thumbnail/motion paths", () => {
    const attachment = {
      ...baseAttachment,
      filename: "中文 #1?.png",
      externalLink: "https://cdn.example.com/photo%20one.png?version=xyz#original",
    };
    expect(getAttachmentUrl(attachment)).toBe(attachment.externalLink);
    for (const build of [getAttachmentThumbnailUrl, getAttachmentMotionClipUrl]) {
      const url = new URL(build(attachment));
      expect(url.origin).toBe(origin);
      expect(decodeURIComponent(url.pathname.split("/").at(-1) || "")).toBe(attachment.filename);
      expect(url.searchParams.has("version")).toBe(false);
      expect(url.hash).toBe("");
    }
  });

  it.each([
    [getAttachmentThumbnailUrl, "thumbnail"],
    [getAttachmentMotionClipUrl, "motion"],
  ] as const)("preserves the encoded share path and token when adding a media selector", (build, selector) => {
    const attachment = {
      ...baseAttachment,
      filename: "DEMO 中文 #1?.png",
      externalLink: `${origin}/file/attachments/test-uid/DEMO%20%E4%B8%AD%E6%96%87%20%231%3F.png?share_token=a%2Bb%2Fc%3D`,
    };
    const url = new URL(build(attachment));
    expect(url.pathname).toBe(new URL(attachment.externalLink).pathname);
    expect(decodeURIComponent(url.pathname.split("/").at(-1) || "")).toBe(attachment.filename);
    expect(url.searchParams.get("share_token")).toBe("a+b/c=");
    expect(url.searchParams.get(selector)).toBe("true");
    expect(url.hash).toBe("");
  });
});

// Regression tests for #6128: share-mode thumbnails/motion clips must keep the share token on externalLink.
describe("attachment URL builders in share mode", () => {
  it("appends thumbnail=true to an externalLink that carries a share token", () => {
    const attachment = {
      ...baseAttachment,
      externalLink: `${origin}/file/attachments/test-uid/photo.png?share_token=abc123`,
    } as Attachment;

    const url = new URL(getAttachmentThumbnailUrl(attachment));
    expect(url.searchParams.get("thumbnail")).toBe("true");
    expect(url.searchParams.get("share_token")).toBe("abc123");
  });

  it("appends motion=true to an externalLink that carries a share token", () => {
    const attachment = {
      ...baseAttachment,
      externalLink: `${origin}/file/attachments/test-uid/photo.png?share_token=abc123`,
    } as Attachment;

    const url = new URL(getAttachmentMotionClipUrl(attachment));
    expect(url.searchParams.get("motion")).toBe("true");
    expect(url.searchParams.get("share_token")).toBe("abc123");
  });

  it("keeps the server thumbnail URL when externalLink has no share token", () => {
    const attachment = {
      ...baseAttachment,
      externalLink: "https://cdn.example.com/photo.png?version=xyz",
    } as Attachment;

    expect(getAttachmentThumbnailUrl(attachment)).toBe(`${origin}/file/attachments/test-uid/photo.png?thumbnail=true`);
  });

  it("keeps the server thumbnail URL when no externalLink is set", () => {
    expect(getAttachmentThumbnailUrl(baseAttachment)).toBe(`${origin}/file/attachments/test-uid/photo.png?thumbnail=true`);
  });

  it("leaves getAttachmentUrl returning the externalLink verbatim", () => {
    const attachment = {
      ...baseAttachment,
      externalLink: `${origin}/file/attachments/test-uid/photo.png?share_token=abc123`,
    } as Attachment;

    expect(getAttachmentUrl(attachment)).toBe(attachment.externalLink);
  });
});

import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";
import {
  advanceJournalWander,
  isJournalCandidate,
  type JournalWanderSession,
  journalDate,
  journalDayRange,
  shuffleJournalNames,
} from "@/lib/journal";
import { State } from "@/types/proto/api/v1/common_pb";
import { MemoSchema } from "@/types/proto/api/v1/memo_service_pb";

const owner = "users/me";
const record = create(MemoSchema, {
  name: "memos/one",
  creator: owner,
  state: State.NORMAL,
  createTime: timestampFromDate(new Date("2026-10-01T20:00:00Z")),
});

describe("personal journal dates", () => {
  it("uses the configured zone across midnight, independently of device settings", () => {
    expect(journalDate(new Date("2026-10-02T20:00:00Z"), "Asia/Shanghai")).toBe("2026-10-03");
    expect(journalDate(new Date("2026-10-02T20:00:00Z"), "America/Los_Angeles")).toBe("2026-10-02");
  });
  it("finds real day boundaries through DST instead of assuming 24 hours", () => {
    const spring = journalDayRange("2026-03-08", "America/New_York");
    const autumn = journalDayRange("2026-11-01", "America/New_York");
    expect(spring && spring.end - spring.start).toBe(23 * 3600);
    expect(autumn && autumn.end - autumn.start).toBe(25 * 3600);
    expect(journalDayRange("2026-10-03", "Asia/Shanghai")).toEqual({
      start: Date.parse("2026-10-02T16:00:00Z") / 1000,
      end: Date.parse("2026-10-03T16:00:00Z") / 1000,
    });
  });
  it("rejects nonexistent or malformed dates", () => {
    expect(journalDayRange("2026-02-30", "Asia/Shanghai")).toBeUndefined();
    expect(journalDayRange("hello", "Asia/Shanghai")).toBeUndefined();
  });
});

describe("journal candidate boundaries", () => {
  const eligible = (memo = record, exclusions = new Set<string>()) =>
    isJournalCandidate(memo, owner, "2026-10-03", "Asia/Shanghai", exclusions);
  it("accepts an owner's saved old original", () => expect(eligible()).toBe(true));
  it("excludes other people, comments, archived records and explicit exclusions", () => {
    expect(eligible({ ...record, creator: "users/someone" })).toBe(false);
    expect(eligible({ ...record, parent: "memos/parent" })).toBe(false);
    expect(eligible({ ...record, state: State.ARCHIVED })).toBe(false);
    expect(eligible(record, new Set([record.name]))).toBe(false);
  });
  it("does not include today, future records or unknown dates", () => {
    expect(eligible({ ...record, createTime: timestampFromDate(new Date("2026-10-02T16:00:00Z")) })).toBe(false);
    expect(eligible({ ...record, createTime: undefined })).toBe(false);
  });
});

describe("random wandering", () => {
  const initial: JournalWanderSession = { names: ["a", "b", "c"], history: ["a"], index: 0, date: "2026-10-03", timezone: "Asia/Shanghai" };
  it("shuffles without changing the collection or repeating records", () => {
    const original = ["a", "b", "c", "a"];
    expect(shuffleJournalNames(original, () => 0)).toEqual(["b", "c", "a"]);
    expect(original).toEqual(["a", "b", "c", "a"]);
  });
  it("skips invalidated future candidates without inventing a previous page", () => {
    const next = advanceJournalWander(initial, new Set(["a", "c"]));
    expect(next.history).toEqual(["a", "c"]);
    expect(next.index).toBe(1);
    expect(next.history[next.index - 1]).toBe("a");
  });
  it("returns to actual forward history after going back and ends without repeats", () => {
    const next = advanceJournalWander(initial, new Set(["a", "c"]));
    expect(advanceJournalWander({ ...next, index: 0 }, new Set(["a", "c"]))).toEqual(next);
    const end = advanceJournalWander(next, new Set(["a", "c"]));
    expect(end.index).toBe(end.history.length);
    expect(end.history).toEqual(["a", "c"]);
  });
  it("supports empty and single-record archives without fabricating content", () => {
    expect(shuffleJournalNames([])).toEqual([]);
    const one = { ...initial, names: ["a"] };
    expect(advanceJournalWander(one, new Set(["a"]))).toMatchObject({ index: 1, history: ["a"] });
  });
});

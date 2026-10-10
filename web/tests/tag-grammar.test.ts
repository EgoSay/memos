import { describe, expect, it } from "vitest";
import { findTagMatches, formatTag } from "@/utils/tag-grammar";

const values = (source: string) => findTagMatches(source).map((match) => match.value);

describe("tag scanner", () => {
  it.each(["读书/标题：副标题", "读书/书名（新版）", "读书/甲、乙、丙", "books/Title:Edition"])("preserves the quoted name %s", (tag) => {
    const source = `#"${tag}"`;
    expect(values(source)).toEqual([tag]);
    expect(formatTag(tag)).toBe(source);
    expect(findTagMatches(source, 0, source.length - 1)).toEqual([]);
  });

  it("keeps bare syntax stable and rejects unrepresentable quoted names", () => {
    expect(formatTag("books/title")).toBe("#books/title");
    expect(values("#books/Title:Edition")).toEqual(["books/Title"]);
    for (const tag of ["", "two words", "book//title", "book/title/", 'quote"inside', "book/<title>", "book/\\name"]) {
      expect(formatTag(tag)).toBeUndefined();
    }
  });

  it.each([
    ["#tag", ["tag"]],
    ["hello#tag", ["tag"]],
    ["#标签", ["标签"]],
    ["#2026", ["2026"]],
    ["#C++", ["C++"]],
    ["#R&D", ["R&D"]],
    ["#-foo #foo- #--- #&&", ["-foo", "foo-", "---", "&&"]],
    ["#work/notes", ["work/notes"]],
    ["#book/", ["book"]],
    ["#/book", []],
    ["#book//fiction", ["book"]],
    ["#book/fiction/", ["book/fiction"]],
    ["#l·l #foo‿bar", ["l·l", "foo‿bar"]],
    ["#tag's #сім'я #O'Brien #O’Brien #OʼBrien", ["tag's", "сім'я", "O'Brien", "O’Brien", "OʼBrien"]],
    ["#café's", ["café's"]],
    ["'#tag' #users' #'missing #rock''roll", ["tag", "users", "rock"]],
    ["#O‘Brien #foo-'bar #foo'1️⃣ #A‍'B", ["O", "foo-", "foo", "A"]],
    ["#foo,bar #price€ #€budget #v²", ["foo", "price", "v"]],
    ["#first#second", ["first", "second"]],
    ["##tag", ["tag"]],
    ["＃tag ﹟tag", []],
  ])("scans %s", (source, expected) => {
    expect(values(source)).toEqual(expected);
  });

  it("emits ignored source code points only in the source span", () => {
    const source = "#A‍B #‍foo #A‌B #‌foo #A️B #́foo #café #foo/́bar";
    expect(values(source)).toEqual(["AB", "foo", "AB", "foo", "AB", "foo", "café", "foo/bar"]);
    expect(findTagMatches("#A‍B")[0]).toMatchObject({ from: 0, to: 4, source: "A‍B", value: "AB" });
    expect(values("#‍‌ #́")).toEqual([]);
  });

  it("matches fully-qualified emoji atomically and excludes components", () => {
    expect(values("#*️⃣ #‼️ #♥ #♥️ #🏻")).toEqual(["*️⃣", "‼️", "♥️"]);
    expect(values("#️⃣ ##️⃣ #first#️⃣")).toEqual(["#️⃣", "first#️⃣"]);
  });

  it("uses the checked-in Unicode 17 and Emoji 17 repertoire", () => {
    expect(values("#꟎ #🫯")).toEqual(["꟎", "🫯"]);
  });

  it("has no tag-specific length limit", () => {
    const tag = "a".repeat(101);
    expect(values(`#${tag}`)).toEqual([tag]);
  });

  it("does not match an emoji across the requested source limit", () => {
    expect(findTagMatches("#😀", 0, 2)).toEqual([]);
  });

  it("does not join an apostrophe across the requested source limit", () => {
    expect(findTagMatches("#O'B", 0, 3).map((match) => match.value)).toEqual(["O"]);
  });
});

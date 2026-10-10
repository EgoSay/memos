import { history, redo, undo } from "@codemirror/commands";
import { markdown } from "@codemirror/lang-markdown";
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { describe, expect, it } from "vitest";
import { createController } from "@/components/MemoEditor/Editor/controller";
import { memoMarkdownExtensions } from "@/utils/memo-markdown-extension";

function view(doc = "") {
  return new EditorView({ state: EditorState.create({ doc }) });
}

describe("source editor controller", () => {
  it("round-trips markdown verbatim, including previously-lossy inputs", () => {
    const v = view();
    const c = createController(v, {} as never);
    for (const md of ["![a](x.png)", "Title\n===", "1. a\n  1. b\n    1. c", "a&nbsp;b"]) {
      c.setMarkdown(md);
      expect(c.getMarkdown()).toBe(md);
    }
  });

  it("reports emptiness on whitespace", () => {
    const c = createController(view("   \n  "), {} as never);
    expect(c.isEmpty()).toBe(true);
  });

  it("inserts markdown as its own block", () => {
    const v = view("alpha");
    const c = createController(v, {} as never);
    v.dispatch({ selection: { anchor: 5 } });
    c.insertMarkdown("beta");
    expect(c.getMarkdown()).toBe("alpha\n\nbeta");
  });

  it("captures and restores the cursor", () => {
    const v = view("alpha beta");
    const c = createController(v, {} as never);
    v.dispatch({ selection: { anchor: 7 } });

    expect(c.getCursor()).toBe(7);

    c.setCursor(99);
    expect(c.getCursor()).toBe(10);
  });
});

function setup(doc: string) {
  const v = new EditorView({
    state: EditorState.create({ doc, extensions: [markdown({ extensions: memoMarkdownExtensions }), history()] }),
  });
  return { v, c: createController(v, {} as never) };
}

describe("starting a tag from the toolbar", () => {
  it.each([
    ["", 0, "#生活"],
    ["今天散步回来", 4, "今天散步 #生活 回来"],
    ["今天散步回来", 6, "今天散步回来 #生活"],
    ["今天散步回来", 0, "#生活 今天散步回来"],
    ["今天 散步", 3, "今天 #生活 散步"],
    ["今天\n回来", 2, "今天 #生活\n回来"],
  ])("keeps subsequent typing in the tag for %s at %i", (doc, cursor, expected) => {
    const { v, c } = setup(doc);
    c.setCursor(cursor);
    c.startTag();
    const position = c.getCursor();
    expect(c.getMarkdown().at(position - 1)).toBe("#");
    v.dispatch(v.state.replaceSelection("生活"));
    expect(c.getMarkdown()).toBe(expected);
    expect(c.getTags()).toEqual(["生活"]);
    v.destroy();
  });

  it("preserves highlighted text and allows undoing just the tag start", () => {
    const doc = "今天散步回来";
    const { v, c } = setup(doc);
    v.dispatch({ selection: { anchor: 2, head: 4 } });
    c.startTag();
    expect(c.getMarkdown()).toBe("今天散步 # 回来");
    expect(c.getCursor()).toBe(6);
    expect(undo(v)).toBe(true);
    expect(c.getMarkdown()).toBe(doc);
    expect(v.state.selection.main).toMatchObject({ anchor: 2, head: 4 });
    v.destroy();
  });
});

describe("suggested tag insertion", () => {
  it("inserts an undoable tag at the selection head without replacing highlighted text", () => {
    const { v, c } = setup("alpha beta");
    v.dispatch({ selection: { anchor: 1, head: 5 } });
    expect(c.insertTag("work")).toBe(true);
    expect(c.getMarkdown()).toBe("alpha #work beta");
    expect(v.state.selection.main).toMatchObject({ anchor: 12, head: 12 });
    expect(c.getTags()).toEqual(["work"]);
    expect(undo(v)).toBe(true);
    expect(c.getMarkdown()).toBe("alpha beta");
    expect(redo(v)).toBe(true);
    expect(c.getTags()).toEqual(["work"]);
    v.destroy();
  });

  it("inserts successive tags at the caret and isolates each click in history", () => {
    const { v, c } = setup("A note\n\n#work");
    c.setCursor(c.getMarkdown().length);
    c.insertTag("release");
    c.insertTag("planning");
    expect(c.getMarkdown()).toBe("A note\n\n#work #release #planning ");
    undo(v);
    expect(c.getMarkdown()).toBe("A note\n\n#work #release ");
    v.destroy();
  });

  it("preserves a punctuation-bearing tag through insertion, an edit, and undo", () => {
    const { v, c } = setup("Note");
    const tag = "读书/书名（新版）";
    c.setCursor(4);
    expect(c.insertTag(tag)).toBe(true);
    expect(c.getMarkdown()).toBe(`Note #"${tag}" `);
    expect(c.getTags()).toEqual([tag]);
    expect(c.getCursor()).toBe(c.getMarkdown().length);
    expect(c.insertTag(tag)).toBe(false);
    expect(undo(v)).toBe(true);
    expect(c.getMarkdown()).toBe("Note");
    expect(redo(v)).toBe(true);
    c.setMarkdown(`${c.getMarkdown()}A later edit.`);
    expect(c.getTags()).toEqual([tag]);
    v.destroy();
  });

  it.each([
    ["", 0, "#work ", 6],
    ["alpha beta", 0, "#work alpha beta", 6],
    ["alpha beta", 6, "alpha #work beta", 12],
    ["alphabeta", 5, "alpha #work beta", 12],
    ["alpha\nnext", 5, "alpha #work \nnext", 12],
    ["alpha beta", 10, "alpha beta #work ", 17],
  ])("inserts inline at position %s / %i with natural spacing", (doc, cursor, expected, nextCursor) => {
    const { v, c } = setup(doc);
    c.setCursor(cursor);
    c.insertTag("work");
    expect(c.getMarkdown()).toBe(expected);
    expect(c.getCursor()).toBe(nextCursor);
    v.destroy();
  });

  it("honors a cursor inside an unfinished code block instead of moving the tag elsewhere", () => {
    const doc = "```js\nconst x = 1";
    const { v, c } = setup(doc);
    c.setCursor(doc.length);
    expect(c.insertTag("work")).toBe(true);
    expect(c.getMarkdown()).toBe(`${doc} #work `);
    expect(c.getTags()).toEqual([]);
    undo(v);
    expect(c.getMarkdown()).toBe(doc);
    v.destroy();
  });

  it("recognizes actual tags, excluding code, links, URLs, escapes, and nested parent names", () => {
    const { v, c } = setup("`#work` [#work](/url) https://example.com/#work \\#work\n\n#work/project");
    expect(c.getTags()).toEqual(["work/project"]);
    c.setCursor(c.getMarkdown().length);
    expect(c.insertTag("work")).toBe(true);
    expect(c.insertTag("work")).toBe(false);
    expect(c.insertTag("two words")).toBe(false);
    expect(c.getTags()).toEqual(["work/project", "work"]);
    v.destroy();
  });
});

describe("checklist detection", () => {
  it.each([
    ["- [ ] Task", true],
    ["- [X] Done", true],
    ["- Parent\n  - [ ] Nested", true],
    ["Note\n\n- [x] Done", true],
    ["```\n- [ ] Example\n```", false],
    ["`- [ ] Example`", false],
    ["    - [ ] Indented code", false],
    ["\\- [ ] Escaped", false],
    ["<pre>\n- [ ] HTML\n</pre>", false],
    ["- Plain list", false],
  ])("recognizes rendered tasks in %s", (doc, expected) => {
    const { v, c } = setup(doc);
    expect(c.hasChecklist()).toBe(expected);
    v.destroy();
  });
});

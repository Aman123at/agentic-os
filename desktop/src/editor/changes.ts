// What changed since the file was saved (PLAN.md M8.10): marks in the gutter
// for added, changed and deleted lines, and the side-by-side compare Code
// shows for Compare with Saved and for a file changed on disk under unsaved
// edits. And Change Review (M8.12): the Agent's change to a file shown inline
// against what it is compared with, Keep or Discard on each hunk. Built on
// @codemirror/merge; this module is its own chunk, fetched when Code first
// opens a file.
import { Change, Chunk, diff, getChunks, getOriginalDoc, MergeView, unifiedMergeView } from "@codemirror/merge";
import { RangeSet, RangeSetBuilder, StateEffect, StateField, type ChangeSpec, type EditorState, type Extension, type Text } from "@codemirror/state";
import { EditorView, gutter, GutterMarker, ViewPlugin, type ViewUpdate } from "@codemirror/view";

export { MergeView };

// Past this many changed characters a diff gives up on precision rather than
// take long; and stops after this long.
const DIFF = { scanLimit: 500, timeout: 200 };

// How long typing has to pause before the gutter catches up.
const SETTLE_MS = 150;

// Lines are diffed as one character each; past this many different lines
// they can't be, and the diff is by character.
const MAX_LINES = 0xffff - 0x800;

// lineChanges diffs two lists of lines: each change replaces whole lines
// [fromA, toA) of a with [fromB, toB) of b. null when there are too many
// different lines to diff this way.
function lineChanges(a: readonly string[], b: readonly string[]): readonly Change[] | null {
  const ids = new Map<string, number>();
  const enc = (lines: readonly string[]) => {
    const out = new Array<string>(lines.length);
    for (let i = 0; i < lines.length; i++) {
      let id = ids.get(lines[i]);
      if (id === undefined) ids.set(lines[i], (id = ids.size));
      out[i] = String.fromCharCode(id < 0xd800 ? id : id + 0x800); // no lone surrogates
    }
    return out.join("");
  };
  const ea = enc(a);
  const eb = enc(b);
  return ids.size > MAX_LINES ? null : diff(ea, eb, DIFF);
}

// lineDiff is a diff of two texts by whole lines, in their characters: what
// the compare shows as its differences.
export function lineDiff(a: string, b: string): readonly Change[] {
  const la = a.split("\n");
  const lb = b.split("\n");
  const lines = lineChanges(la, lb);
  if (!lines) return diff(a, b, DIFF);
  const starts = (ls: string[]) => {
    const out = [0];
    for (const l of ls) out.push(out[out.length - 1] + l.length + 1);
    return out;
  };
  const sa = starts(la);
  const sb = starts(lb);
  // A run of lines takes the line break after each; one that reaches the end
  // has none after its last line, so it takes the one before its first.
  return lines.map((c) => {
    const end = c.toA === la.length;
    const [fromA, toA] = end ? [Math.max(0, sa[c.fromA] - 1), a.length] : [sa[c.fromA], sa[c.toA]];
    const [fromB, toB] = end ? [Math.max(0, sb[c.fromB] - 1), b.length] : [sb[c.fromB], sb[c.toB]];
    return new Change(c.fromA === 0 && end ? 0 : fromA, toA, c.fromB === 0 && end ? 0 : fromB, toB);
  });
}

type Kind = "added" | "changed" | "deleted";

class Mark extends GutterMarker {
  constructor(readonly kind: Kind) {
    super();
    this.elementClass = `cm-change cm-change--${kind}`;
  }
  eq(other: GutterMarker): boolean {
    return other instanceof Mark && other.kind === this.kind;
  }
}
const MARKS: Record<Kind, Mark> = { added: new Mark("added"), changed: new Mark("changed"), deleted: new Mark("deleted") };

// marksFor marks doc's lines where it differs from saved: added and changed
// lines each, and deleted ones on the line after them (or the last line).
function marksFor(saved: Text, doc: Text): RangeSet<Mark> {
  if (saved === doc || saved.eq(doc)) return RangeSet.empty;
  const b = new RangeSetBuilder<Mark>();
  const mark = (kind: Kind, first: number, last: number) => {
    for (let n = Math.max(1, first); n <= Math.min(last, doc.lines); n++) b.add(doc.line(n).from, doc.line(n).from, MARKS[kind]);
  };
  const lines = lineChanges(saved.toJSON(), doc.toJSON());
  if (lines) {
    for (const c of lines) {
      if (c.fromA === c.toA) mark("added", c.fromB + 1, c.toB);
      else if (c.fromB === c.toB) mark("deleted", c.fromB + 1, c.fromB + 1);
      else mark("changed", c.fromB + 1, c.toB);
    }
  } else {
    for (const c of Chunk.build(saved, doc, DIFF)) {
      const first = doc.lineAt(Math.min(c.fromB, doc.length)).number;
      if (c.fromA === c.toA) mark("added", first, doc.lineAt(Math.min(c.endB, doc.length)).number);
      else if (c.fromB === c.toB) mark("deleted", first, first);
      else mark("changed", first, doc.lineAt(Math.min(c.endB, doc.length)).number);
    }
  }
  return b.finish();
}

// setSaved tells the marks the file was saved (or opened again) as this text.
export const setSaved = StateEffect.define<Text>();
const setMarks = StateEffect.define<RangeSet<Mark>>();

interface Changes {
  saved: Text;
  marks: RangeSet<Mark>;
}

// The saved text, and the marks as last worked out, moved along with edits
// until they are worked out again.
const changes = StateField.define<Changes>({
  // A new editor state is made from the file as opened: nothing changed yet.
  create: (s) => ({ saved: s.doc, marks: RangeSet.empty }),
  update(v, tr) {
    let { saved, marks } = v;
    if (tr.docChanged) marks = marks.map(tr.changes);
    for (const e of tr.effects) {
      if (e.is(setSaved)) saved = e.value;
      if (e.is(setMarks)) marks = e.value;
    }
    return saved === v.saved && marks === v.marks ? v : { saved, marks };
  },
});

// The marks are worked out again once typing pauses, and at once when the
// state is shown or saved.
const settle = ViewPlugin.fromClass(
  class {
    timer?: ReturnType<typeof setTimeout>;
    constructor(readonly view: EditorView) {
      this.schedule(0);
    }
    update(u: ViewUpdate) {
      if (u.docChanged) this.schedule(SETTLE_MS);
      else if (u.startState.field(changes).saved !== u.state.field(changes).saved) this.schedule(0);
    }
    schedule(ms: number) {
      clearTimeout(this.timer);
      this.timer = setTimeout(() => {
        const { saved, marks } = this.view.state.field(changes);
        const next = marksFor(saved, this.view.state.doc);
        if (next.size || marks.size) this.view.dispatch({ effects: setMarks.of(next) });
      }, ms);
    }
    destroy() {
      clearTimeout(this.timer);
    }
  },
);

// changeMarks is the gutter of changes against the saved text: that of the
// state it joins unless given, as when it joins a state already edited.
// onClick is told the line clicked when it holds a mark.
export function changeMarks(saved: Text | undefined, onClick: (line: number) => void): Extension {
  return [
    saved ? changes.init(() => ({ saved, marks: RangeSet.empty })) : changes,
    settle,
    gutter({
      class: "cm-savedGutter",
      markers: (v) => v.state.field(changes).marks,
      domEventHandlers: {
        mousedown(view, block) {
          let marked = false;
          view.state.field(changes).marks.between(block.from, block.from, () => {
            marked = true;
          });
          if (!marked) return false;
          onClick(view.state.doc.lineAt(block.from).number);
          return true;
        },
      },
    }),
  ];
}

// replaceWith is the smallest change turning doc into text, so undo and the
// cursor survive a whole-file replacement.
export function replaceWith(doc: Text, text: string): ChangeSpec[] {
  const a = doc.toString();
  return diff(a, text, DIFF).map((c) => ({ from: c.fromA, to: c.toA, insert: text.slice(c.fromB, c.toB) }));
}

// The gutter's look: a bar beside added and changed lines, a wedge where
// lines were deleted.
export const changeTheme = EditorView.theme({
  ".cm-savedGutter": { width: "4px", paddingLeft: "2px" },
  ".cm-savedGutter .cm-gutterElement": { padding: "0", cursor: "pointer" },
  ".cm-change--added": { background: "var(--code-tag)", backgroundClip: "content-box" },
  ".cm-change--changed": { background: "var(--accent)", backgroundClip: "content-box" },
  ".cm-change--deleted": {
    background: "linear-gradient(135deg, var(--danger) 0 35%, transparent 35%) no-repeat",
    backgroundSize: "8px 8px",
  },
});

// ------------------------------------------------------------ Change Review

export interface Review {
  // What the file is compared with: its before-image with the hunks kept.
  base: Text;
  // The base does not exist (the Agent created the file): Discard removes
  // the file rather than empty it.
  gone: boolean;
  // Told when a hunk is kept, with what the file is compared with now.
  kept: (base: Text) => void;
  // Told when a hunk's old lines are put back in the editor.
  discarded: () => void;
  discardFile: () => void;
}

// review shows a file's changes inline against r.base, with Keep and Discard
// on each hunk.
export function review(r: Review): Extension {
  return [
    unifiedMergeView({
      original: r.base,
      diffConfig: { override: lineDiff },
      highlightChanges: false,
      mergeControls: (type, action) => {
        const b = document.createElement("button");
        b.className = `cm-review-btn cm-review-btn--${type === "accept" ? "keep" : "discard"}`;
        b.textContent = type === "accept" ? "Keep" : "Discard";
        b.setAttribute("aria-label", type === "accept" ? "Keep this change" : "Discard this change");
        // It acts on click, so a keyboard press works too.
        b.addEventListener("mousedown", (e) => e.preventDefault());
        b.addEventListener("click", (e) => (type === "reject" && r.gone ? r.discardFile() : action(e)));
        return b;
      },
    }),
    EditorView.updateListener.of((u) => {
      for (const tr of u.transactions) {
        if (tr.isUserEvent("accept")) r.kept(getOriginalDoc(u.state));
        if (tr.isUserEvent("revert")) r.discarded();
      }
    }),
    reviewTheme,
  ];
}

// hunksLeft is how many changes are left to decide in a review.
export function hunksLeft(state: EditorState): number {
  return getChunks(state)?.chunks.length ?? 0;
}

const reviewTheme = EditorView.theme({
  ".cm-changedLine": { backgroundColor: "rgba(46, 160, 67, 0.12) !important" },
  ".cm-deletedChunk": { backgroundColor: "rgba(217, 48, 37, 0.1) !important", position: "relative", minHeight: "1.6em" },
  ".cm-deletedChunk .cm-chunkButtons": { position: "absolute", insetInlineEnd: "6px", top: "2px", zIndex: "1" },
  ".cm-review-btn": { border: "none", borderRadius: "4px", margin: "0 2px", padding: "1px 8px", color: "#fff", font: "inherit", fontSize: "11.5px", cursor: "pointer" },
  ".cm-review-btn--keep": { background: "#2ea043" },
  ".cm-review-btn--discard": { background: "var(--danger)" },
});

// A side-by-side compare over one open file (PLAN.md M8.10): the saved
// version, or the version on disk, on the left; the tab's own text on the
// right. Every edit on the right — typed, or a hunk taken from the left — goes
// straight into the tab, so its undo history keeps it.
//
// Comparing with what changed on disk, each difference has Take theirs (the
// left side's lines into the tab) and Keep mine (the left side is only a
// scratch copy, so it takes the tab's lines and the difference is gone); Done
// makes the disk's version the one the tab's unsaved changes are against.
import { EditorState, type ChangeSet, type Extension, type Text } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { basicSetup } from "codemirror";
import { useEffect, useRef, useState } from "react";

import { lineDiff, MergeView, replaceWith } from "../../editor/changes";

export interface CompareProps {
  kind: "saved" | "disk";
  name: string;
  left: Text; // the saved version, or the one on disk as the person has decided it so far
  right: Text; // the tab's text now
  line?: number; // the line of the tab's text to show first
  extensions: Extension; // the tab's language and look
  onLeft: (text: Text) => void;
  onRight: (changes: ChangeSet) => void;
  onDone: () => void;
  onClose: () => void;
}

export default function Compare({ kind, name, left, right, line, extensions, onLeft, onRight, onDone, onClose }: CompareProps) {
  const host = useRef<HTMLDivElement>(null);
  const mv = useRef<MergeView | null>(null);
  const [count, setCount] = useState(0);
  const cb = useRef({ onLeft, onRight });
  cb.current = { onLeft, onRight };

  useEffect(() => {
    if (!host.current) return;
    const disk = kind === "disk";
    // copy puts one side's lines of a difference over the other's, as
    // MergeView's own revert does.
    const copy = (i: number, toLeft: boolean) => {
      const m = mv.current;
      const c = m?.chunks[i];
      if (!m || !c) return;
      const [src, dest, sFrom, sTo, dFrom, dTo] = toLeft ? [m.b, m.a, c.fromB, c.toB, c.fromA, c.toA] : [m.a, m.b, c.fromA, c.toA, c.fromB, c.toB];
      let insert = src.state.sliceDoc(sFrom, Math.max(sFrom, sTo - 1));
      if (sFrom !== sTo && dTo <= dest.state.doc.length) insert += src.state.lineBreak;
      dest.dispatch({ changes: { from: dFrom, to: Math.min(dest.state.doc.length, dTo), insert }, userEvent: "revert" });
    };
    const button = (label: string, text: string, cls: string, act: (i: number) => void) => {
      const b = document.createElement("button");
      b.className = `code-compare__act ${cls}`;
      b.textContent = text;
      b.title = label;
      b.setAttribute("aria-label", label);
      // MergeView acts on mousedown in this column; these act on click, so a
      // keyboard press works too.
      b.addEventListener("mousedown", (e) => {
        e.preventDefault();
        e.stopPropagation();
      });
      b.addEventListener("click", () => act(Number(b.parentElement?.dataset.chunk)));
      return b;
    };
    const m = new MergeView({
      parent: host.current,
      a: {
        doc: left,
        extensions: [basicSetup, extensions, compareTheme, EditorState.readOnly.of(true), EditorView.editable.of(false), EditorView.updateListener.of((u) => u.docChanged && cb.current.onLeft(u.state.doc)), counter()],
      },
      b: {
        doc: right,
        extensions: [basicSetup, extensions, compareTheme, EditorView.updateListener.of((u) => u.docChanged && cb.current.onRight(u.changes)), counter()],
      },
      revertControls: "a-to-b",
      renderRevertControl: () => {
        const div = document.createElement("div");
        div.className = "code-compare__acts";
        if (disk) {
          div.append(button("Take theirs", "→", "code-compare__act--take", (i) => copy(i, false)), button("Keep mine", "✓", "code-compare__act--keep", (i) => copy(i, true)));
        } else {
          div.append(button("Revert to saved", "→", "code-compare__act--take", (i) => copy(i, false)));
        }
        return div;
      },
      gutter: true,
      // Differences are whole lines, as the gutter marks them.
      diffConfig: { override: lineDiff },
      highlightChanges: false,
    });
    mv.current = m;
    setCount(m.chunks.length);
    if (line) {
      const at = m.b.state.doc.line(Math.min(Math.max(1, line), m.b.state.doc.lines)).from;
      m.b.dispatch({ selection: { anchor: at }, effects: EditorView.scrollIntoView(at, { y: "center" }) });
    }
    m.b.focus();
    return () => {
      m.destroy();
      mv.current = null;
    };
    // Made once per compare; the Workspace remounts it for another.
  }, []);

  // counter keeps the count of differences shown up to date.
  function counter(): Extension {
    return EditorView.updateListener.of(() => {
      if (mv.current) setCount(mv.current.chunks.length);
    });
  }

  // takeAll makes the tab's text the left side's: every remaining difference
  // taken from it.
  const takeAll = () => {
    const m = mv.current;
    if (m) m.b.dispatch({ changes: replaceWith(m.b.state.doc, m.a.state.doc.toString()), userEvent: "revert" });
  };

  const disk = kind === "disk";
  const noun = count === 1 ? "difference" : "differences";
  return (
    <div className="code-compare" role="region" aria-label={disk ? `${name} changed on disk` : `${name} compared with saved`}>
      <div className="code-compare__bar">
        <span className="code-compare__side">{disk ? "On disk (theirs)" : "Saved"}</span>
        <span className="code-compare__what" aria-live="polite">
          {count === 0 ? (disk ? "All differences decided." : "No changes since saved.") : `${count} ${noun}`}
        </span>
        {disk ? (
          <>
            {count > 0 && (
              <button className="finder__btn" onClick={takeAll}>
                Take All Theirs
              </button>
            )}
            <button className="finder__btn finder__btn--on" onClick={onDone}>
              {count > 0 ? "Keep the Rest Mine" : "Done"}
            </button>
            <button className="finder__btn" onClick={onClose} title="Close the compare; the file stays changed on disk">
              Cancel
            </button>
          </>
        ) : (
          <>
            {count > 0 && (
              <button className="finder__btn" onClick={takeAll}>
                Revert All
              </button>
            )}
            <button className="finder__btn finder__btn--on" onClick={onClose}>
              Close
            </button>
          </>
        )}
        <span className="code-compare__side code-compare__side--right">Yours</span>
      </div>
      <div className="code-compare__view" ref={host} />
    </div>
  );
}

// Differences read in both the light and the dark theme.
const compareTheme = EditorView.theme({
  "&": { height: "auto" },
  "&.cm-merge-a .cm-changedLine, .cm-deletedChunk": { backgroundColor: "rgba(217, 48, 37, 0.1)" },
  "&.cm-merge-b .cm-changedLine": { backgroundColor: "rgba(46, 160, 67, 0.12)" },
  "&.cm-merge-a .cm-changedText": { background: "rgba(217, 48, 37, 0.28)" },
  "&.cm-merge-b .cm-changedText": { background: "rgba(46, 160, 67, 0.3)" },
  "&.cm-merge-a .cm-changedLineGutter": { background: "var(--danger)" },
  "&.cm-merge-b .cm-changedLineGutter": { background: "var(--code-tag)" },
});

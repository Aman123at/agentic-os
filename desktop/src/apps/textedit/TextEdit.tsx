// TextEdit (PLAN.md §4.3): one text file per window, edited in CodeMirror and
// read and written through FileService as the aos user, up to 1 MiB. Before a
// save it compares the file's size and modification time with the version it
// opened, and asks rather than overwrite a change made meanwhile (by an Agent,
// a Terminal or another window). ⌘S or Ctrl+S saves. A window with no file is a
// new document, saved to a path it asks for.
import { Code, ConnectError } from "@connectrpc/connect";
import { Compartment, EditorState, type Extension, type Text } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { basicSetup } from "codemirror";
import { useCallback, useContext, useEffect, useRef, useState } from "react";

import { files } from "../../api/client";
import type { FileInfo } from "../../gen/aos/v1/services_pb";
import { WinContext, useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { formatSize, looksBinary } from "../finder/fs";
import { languageFor } from "./languages";

// The largest file TextEdit opens or saves; FileService.Read returns at most
// this much at once.
const MAX_BYTES = 1 << 20;

const theme = EditorView.theme({
  "&": { height: "100%", backgroundColor: "var(--window-bg)", color: "var(--text)", fontSize: "13px" },
  ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: "1.5" },
  ".cm-gutters": { backgroundColor: "var(--panel)", color: "var(--muted)", borderRight: "1px solid var(--border)" },
  ".cm-activeLineGutter, .cm-activeLine": { backgroundColor: "rgba(127, 127, 127, 0.1)" },
  ".cm-cursor": { borderLeftColor: "var(--text)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": { backgroundColor: "rgba(80, 140, 255, 0.3)" },
});

type Load = { kind: "loading" } | { kind: "ready" } | { kind: "refused"; message: string };

// stamp identifies a version of a file on disk.
function stamp(info?: FileInfo): string {
  return info ? `${info.size}:${info.modifiedAt?.seconds ?? 0}:${info.modifiedAt?.nanos ?? 0}` : "";
}

export default function TextEdit() {
  const winId = useContext(WinContext);
  const [path, setPath] = useWinState("path", "");
  const setWinState = useDesktop((s) => s.setWinState);
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const language = useRef(new Compartment());
  const savedDoc = useRef<Text | null>(null); // the text as last opened or saved
  const onDisk = useRef(""); // the stamp of the version that text came from
  const loaded = useRef<string | null>(null); // the path the editor holds
  const loadSeq = useRef(0);
  const [load, setLoad] = useState<Load>({ kind: "ready" });
  const [edited, setEdited] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [saveAs, setSaveAs] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const extensions = useCallback(
    (): Extension[] => [
      basicSetup,
      theme,
      language.current.of([]),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) setEdited(!savedDoc.current || !u.state.doc.eq(savedDoc.current));
      }),
    ],
    [],
  );

  // The editor lives as long as the window.
  useEffect(() => {
    const v = new EditorView({ parent: host.current!, state: EditorState.create({ doc: "", extensions: extensions() }) });
    view.current = v;
    savedDoc.current = v.state.doc;
    return () => {
      v.destroy();
      view.current = null;
    };
  }, [extensions]);

  // The window's close button asks before discarding unsaved changes.
  useEffect(() => setWinState(winId, { edited: edited ? "1" : "" }), [edited, winId, setWinState]);

  const open = useCallback(
    async (p: string) => {
      const seq = ++loadSeq.current;
      const v = view.current;
      if (!v) return;
      loaded.current = p;
      setLoad({ kind: "loading" });
      setConflict(false);
      setError("");
      const name = p.slice(p.lastIndexOf("/") + 1);
      try {
        const info = (await files.stat({ path: p })).info;
        if (seq !== loadSeq.current) return;
        if (!info || info.dir) return setLoad({ kind: "refused", message: `${name} is a folder.` });
        if (info.size > BigInt(MAX_BYTES)) {
          return setLoad({ kind: "refused", message: `${name} is ${formatSize(info.size, false)}. TextEdit opens files up to 1 MB.` });
        }
        const bytes = (await files.read({ path: p, offset: 0n, limit: BigInt(MAX_BYTES) })).content;
        if (seq !== loadSeq.current) return;
        let text: string;
        try {
          if (looksBinary(bytes)) throw new Error("binary");
          text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
        } catch {
          return setLoad({ kind: "refused", message: `${name} is not a text file TextEdit can edit.` });
        }
        v.setState(EditorState.create({ doc: text, extensions: extensions() }));
        savedDoc.current = v.state.doc;
        onDisk.current = stamp(info);
        setEdited(false);
        setLoad({ kind: "ready" });
        const lang = await languageFor(name).catch(() => null);
        if (lang && seq === loadSeq.current) v.dispatch({ effects: language.current.reconfigure(lang) });
      } catch (err) {
        if (seq === loadSeq.current) setLoad({ kind: "refused", message: ConnectError.from(err).message });
      }
    },
    [extensions],
  );

  useEffect(() => {
    if (path && path !== loaded.current) void open(path);
  }, [path, open]);

  // write saves the editor's text to p. Unless forced, it first checks that
  // the file on disk is still the version the text came from. A new document
  // (create) never replaces a file without asking.
  const write = useCallback(
    async (p: string, opts: { force?: boolean; create?: boolean } = {}) => {
      const v = view.current;
      if (!v || busy) return;
      const doc = v.state.doc;
      const bytes = new TextEncoder().encode(doc.toString());
      if (bytes.length > MAX_BYTES) return setError("TextEdit saves files up to 1 MB.");
      setBusy(true);
      setError("");
      try {
        if (!opts.force && !opts.create) {
          const now = await files.stat({ path: p }).then(
            (r) => stamp(r.info),
            (err) => {
              if (ConnectError.from(err).code === Code.NotFound) return onDisk.current; // deleted meanwhile: save it again
              throw err;
            },
          );
          if (now !== onDisk.current) return setConflict(true);
        }
        try {
          await files.write({ path: p, content: bytes, overwrite: !opts.create });
        } catch (err) {
          if (!opts.create || ConnectError.from(err).code !== Code.AlreadyExists) throw err;
          if (!window.confirm(`${p} already exists. Replace it?`)) return;
          await files.write({ path: p, content: bytes, overwrite: true });
        }
        onDisk.current = stamp((await files.stat({ path: p })).info);
        savedDoc.current = doc;
        loaded.current = p;
        setConflict(false);
        setEdited(!v.state.doc.eq(doc));
        if (p !== path) setPath(p);
      } catch (err) {
        setError(ConnectError.from(err).message);
      } finally {
        setBusy(false);
      }
    },
    [busy, path, setPath],
  );

  const save = useCallback(() => {
    if (load.kind !== "ready") return;
    if (!path) setSaveAs((s) => s ?? "~/Untitled.txt");
    else void write(path);
  }, [load.kind, path, write]);

  return (
    <div
      className="textedit"
      onKeyDownCapture={(e) => {
        if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === "s") {
          e.preventDefault();
          e.stopPropagation();
          save();
        }
      }}
    >
      <div className="textedit__bar">
        <span className="textedit__path" title={path}>
          {path || "Untitled"}
        </span>
        {edited && <span className="textedit__edited">Edited</span>}
        <button className="finder__btn" disabled={busy || load.kind !== "ready"} onClick={save} title="Save (⌘S)">
          {busy ? "Saving…" : "Save"}
        </button>
      </div>

      {saveAs !== null && (
        <form
          className="textedit__saveas"
          onSubmit={(e) => {
            e.preventDefault();
            const p = saveAs.trim();
            if (!p) return;
            setSaveAs(null);
            void write(p, { create: true });
          }}
        >
          <label>
            Save as{" "}
            <input className="textedit__input" aria-label="Save as" autoFocus value={saveAs} onChange={(e) => setSaveAs(e.target.value)} />
          </label>
          <button className="finder__btn finder__btn--on" type="submit">
            Save
          </button>
          <button className="finder__btn" type="button" onClick={() => setSaveAs(null)}>
            Cancel
          </button>
        </form>
      )}

      {conflict && (
        <div className="textedit__conflict" role="alertdialog" aria-label="The file changed on disk">
          <span>
            {path.slice(path.lastIndexOf("/") + 1)} changed on disk since you opened it. Overwrite it with your version, or revert to the version on disk?
          </span>
          <button className="finder__btn tasks__btn--danger" onClick={() => void write(path, { force: true })}>
            Overwrite
          </button>
          <button className="finder__btn" onClick={() => void open(path)}>
            Revert
          </button>
          <button className="finder__btn" onClick={() => setConflict(false)}>
            Cancel
          </button>
        </div>
      )}

      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      <div className="textedit__editor" ref={host} hidden={load.kind !== "ready"} />
      {load.kind === "loading" && <div className="preview__note">Loading…</div>}
      {load.kind === "refused" && <div className="preview__note">{load.message}</div>}
    </div>
  );
}

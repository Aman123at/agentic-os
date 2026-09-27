// One Code window's Workspace (PLAN.md M8.3): the Explorer, the tabs, the
// editor and the status bar over a folder aosd watches for this tab.
//
// Each open file is a Doc. The window has one CodeMirror view and swaps each
// Doc's EditorState in and out of it as tabs change, so undo history and the
// selection stay with the file. A Doc remembers the text last opened or saved
// and the version of the file on disk it came from (its stamp): a save checks
// the stamp first and asks rather than overwrite a change made meanwhile, and
// the Workspace's watch reloads a clean tab when its file changes on disk.
// The gutter marks what changed since that text (M8.10), and a side-by-side
// compare shows it, or settles a change on disk under unsaved edits hunk by
// hunk.
import { indentUnit } from "@codemirror/language";
import { Compartment, EditorState, type ChangeSet, type Extension, type Text, type TransactionSpec } from "@codemirror/state";
import { EditorView, type ViewUpdate } from "@codemirror/view";
import { Code as RpcCode, ConnectError } from "@connectrpc/connect";
import { basicSetup } from "codemirror";
import { lazy, Suspense, useCallback, useContext, useEffect, useRef, useState } from "react";

import { code, files, tasks as taskApi } from "../../api/client";
import { friendlyError } from "../../api/error";
import { sensitiveOf } from "../../api/sensitive";
import { openWorkspace, type WorkspaceHandle } from "../../api/workspaces";
import { detect, firstLine, languageName } from "../../editor/detect";
import { detectIndent, MAX_TEXT, readText, stamp, type Indent } from "../../editor/document";
import { languageFor } from "../../editor/languages";
import { highlight, theme } from "../../editor/theme";
import {
  FileChange_Kind,
  WorkspaceWatch_State,
  type FileInfo,
  type OpenWorkspaceResponse,
  type SensitiveBackup,
  type SensitivePath,
  type WorkspaceChanged,
  type WorkspaceWatch,
  type EditorContext,
  type TaskFileChange,
} from "../../gen/aos/v1/services_pb";
import { TaskState } from "../../gen/aos/v1/types_pb";
import { setCloseGuard } from "../../shell/closeGuard";
import { WinContext, useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { SensitiveConfirm, type SensitiveAsk } from "../../ui/SensitiveConfirm";
import { Splitter } from "../../ui/Splitter";
import { fileKind } from "../filetypes";
import { formatSize } from "../finder/fs";
import { ChangeList, type CommandChange } from "./ChangeList";
import { CodeAgent, type Added } from "./CodeAgent";
import { Explorer, type Relist } from "./Explorer";
import { baseOf, dirOf, relative, within } from "./paths";
import { completion, modClick, wordAt } from "./navigate";
import { Outline } from "./Outline";
import { QuickOpen, symbolItem, type PaletteItem, type Preset, type Reveal } from "./QuickOpen";
import { SearchView, type SearchRequest } from "./SearchView";

// The Terminal panel brings xterm with it, so it loads when first opened (M8.6).
const TerminalPanel = lazy(() => import("./TerminalPanel"));
// @codemirror/merge comes with the first file opened (M8.10).
const Compare = lazy(() => import("./Compare"));
type ChangesModule = typeof import("../../editor/changes");

// AUTO_SAVE_MS is how long typing has to pause before Auto Save saves.
const AUTO_SAVE_MS = 1000;

type Load = "loading" | "ready" | "binary" | "too-big" | "error";

interface Doc {
  path: string;
  load: Load;
  note: string; // why a Doc that isn't ready can't be shown
  state?: EditorState; // while the Doc is not the one in the view
  saved?: Text; // the text as last opened or saved
  stamp: string; // the version on disk that text came from
  dirty: boolean;
  saving: boolean;
  error: string; // the last save's failure
  // A change on disk this tab has not taken: the file changed under unsaved
  // edits, or went away.
  disk?: "changed" | "deleted";
  readOnly: string; // why the file can't be edited, or ""
  // In Root Mode, what can go wrong changing this Sensitive System File (M8.5):
  // saving asks first, Auto Save leaves it alone, and the versions confirmed
  // saves replaced can be restored.
  sensitive: string;
  backups: SensitiveBackup[];
  indent: Indent;
  crlf: boolean;
  first: string; // the first line as opened: its #! names the language when the name doesn't
  lang: Extension | null;
  timer?: ReturnType<typeof setTimeout>;
  // Where to put the cursor once the Doc has loaded: a jump from Quick Open
  // or a search result to a file not yet open.
  reveal?: Reveal;
  // The compare shown in place of the editor (M8.10): with the saved text, or
  // with the version on disk (theirs) the tab's unsaved edits are settling
  // against. left is that side as decided so far.
  compare?: { kind: "saved" | "disk"; left: Text; line?: number; theirs?: { text: Text; stamp: string } };
  // Change Review (M8.12): the file shown inline against what it is compared
  // with — a Task's before-image with the hunks kept, or for a command's
  // change what the editor had (kept only here) — read-only meanwhile.
  review?: { taskId: string; base: Text; gone: boolean; command?: boolean };
}

// A place navigation can come back to (⌃- and ⌃⇧-).
interface Loc {
  path: string;
  pos: number;
}

// How far back navigation remembers.
const HISTORY = 50;

// Ask is the unsaved-files dialog: for closing tabs or the window.
interface Ask {
  paths: string[];
  then: () => void;
}

export function Workspace({ path, onClose }: { path: string; onClose: () => void }) {
  const winId = useContext(WinContext);
  const setWinTitle = useDesktop((s) => s.setWinTitle);
  const setWinState = useDesktop((s) => s.setWinState);
  const closeWindow = useDesktop((s) => s.closeWindow);
  const openFile = useDesktop((s) => s.openFile);
  const revealInFinder = useDesktop((s) => s.revealInFinder);
  const [savedTabs] = useWinState("tabs", "");
  const [savedActive] = useWinState("active", "");
  const [openReq] = useWinState("open", "");
  const [autoSaveSetting, setAutoSave] = useWinState("autosave", "");
  const [sideSetting, setSide] = useWinState("side", "240");
  const [sideView, setSideView] = useWinState("view", "explorer");
  const [palette, setPalette] = useState<{ initial: string; preset?: Preset } | null>(null);
  const [notice, setNotice] = useState("");
  const [searchReq, setSearchReq] = useState<SearchRequest>({ n: 0 });
  const [panelSetting, setPanel] = useWinState("panel", "");
  const [panelHeight, setPanelHeight] = useWinState("panelh", "220");
  const panelOpen = panelSetting === "1";
  // The Agent sidebar (M8.11): its width, 0 when hidden.
  const [agentSetting, setAgentWidth] = useWinState("agentw", "0");
  const agentWidth = Number(agentSetting) || 0;
  const [added, setAdded] = useState<Added | null>(null);
  const [agentFocus, setAgentFocus] = useState(0);
  // Once opened, the panel stays mounted while hidden, so its shells keep
  // running and their output keeps arriving.
  const [panelMounted, setPanelMounted] = useState(panelOpen);

  const [ws, setWs] = useState<OpenWorkspaceResponse | null>(null);
  const [wsError, setWsError] = useState("");
  const [watch, setWatch] = useState<WorkspaceWatch | undefined>();
  const [order, setOrder] = useState<string[]>([]);
  const orderRef = useRef(order);
  orderRef.current = order;
  const [active, setActive] = useState("");
  const [cursor, setCursor] = useState({ line: 1, col: 1 });
  const [error, setError] = useState("");
  const [ask, setAsk] = useState<Ask | null>(null);
  const [sensitive, setSensitive] = useState<{ ask: SensitiveAsk; confirm: () => void } | null>(null);
  const [, setTick] = useState(0);
  const bump = useCallback(() => setTick((n) => n + 1), []);

  const docs = useRef(new Map<string, Doc>());
  const rootEl = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const host = useRef<HTMLDivElement>(null);
  const shown = useRef(""); // the Doc whose state is in the view
  const activeRef = useRef("");
  const handle = useRef<WorkspaceHandle | null>(null);
  const relisters = useRef(new Set<Relist>());
  const compartments = useRef({ lang: new Compartment(), indent: new Compartment(), readOnly: new Compartment(), marks: new Compartment(), review: new Compartment() });
  const changesMod = useRef<ChangesModule | null>(null);
  const restored = useRef(false);

  const autoSave = autoSaveSetting ? autoSaveSetting === "on" : !ws?.rootMode;
  const autoSaveRef = useRef(autoSave);
  autoSaveRef.current = autoSave;

  const relist = useCallback((dirs: string[] | "all") => relisters.current.forEach((r) => r(dirs)), []);
  const subscribe = useCallback((r: Relist) => {
    relisters.current.add(r);
    return () => void relisters.current.delete(r);
  }, []);

  // ------------------------------------------------------------ doc state

  const current = (d: Doc): EditorState | undefined => (shown.current === d.path ? (view.current?.state ?? d.state) : d.state);

  const apply = (d: Doc, spec: TransactionSpec) => {
    if (shown.current === d.path && view.current) view.current.dispatch(spec);
    else if (d.state) d.state = d.state.update(spec).state;
  };

  // editable is whether a Doc can be edited: not a read-only file, and not
  // while its changes are reviewed.
  const editable = (d: Doc): Extension => [EditorState.readOnly.of(Boolean(d.readOnly) || Boolean(d.review)), EditorView.editable.of(!d.readOnly)];

  const extensions = (d: Doc): Extension[] => {
    const c = compartments.current;
    return [
      basicSetup,
      theme,
      highlight,
      c.lang.of(d.lang ?? []),
      c.indent.of([indentUnit.of(d.indent.tabs ? "\t" : " ".repeat(d.indent.size)), EditorState.tabSize.of(d.indent.tabs ? d.indent.size : 4)]),
      c.readOnly.of(editable(d)),
      c.marks.of(marks()),
      c.review.of(reviewExt(d)),
      d.crlf ? EditorState.lineSeparator.of("\r\n") : [],
      EditorView.updateListener.of((u) => onUpdate.current(u)),
      // Navigation (M8.8): ⌘-click to a definition, and completion from the
      // language's keywords and the Workspace's symbols.
      modClick((pos) => void definitionRef.current(pos)),
      completion(() => (wsRef.current ? { workspaceId: wsRef.current.id, root: wsRef.current.root, path: d.path } : null), detect(d.path, d.first)),
    ];
  };

  // marks is the gutter of changes since saved, once its module is here:
  // against the text a state was made from, or saved when given.
  const marks = (saved?: Text): Extension => {
    const m = changesMod.current;
    return m ? [m.changeTheme, m.changeMarks(saved, (line) => compareRef.current(line))] : [];
  };

  // loadMarks fetches the gutter's module once, and gives it to the files
  // already open.
  const loadMarks = async () => {
    if (changesMod.current) return;
    const m = await import("../../editor/changes").catch(() => null);
    if (!m || changesMod.current) return;
    changesMod.current = m;
    for (const d of docs.current.values()) if (d.load === "ready" && d.saved) apply(d, { effects: compartments.current.marks.reconfigure(marks(d.saved)) });
  };

  // reviewExt is a Doc's Change Review, while it has one.
  const reviewExt = (d: Doc): Extension => {
    const m = changesMod.current;
    const r = d.review;
    if (!m || !r) return [];
    // Told from inside an editor update, so each acts a moment later.
    const later = (f: () => void) => setTimeout(f);
    return m.review({
      base: r.base,
      gone: r.gone,
      kept: (base) => later(() => void reviewActs.current.kept(d, base)),
      discarded: () => later(() => void reviewActs.current.discarded(d)),
      discardFile: () => later(() => void reviewActs.current.discardWhole(d)),
    });
  };

  // markSaved tells a Doc's gutter the text it is now compared with.
  const markSaved = (d: Doc, text: Text) => {
    const m = changesMod.current;
    if (m) apply(d, { effects: m.setSaved.of(text) });
  };

  // show puts the active Doc's state in the view, keeping the one it replaces.
  const show = useCallback(() => {
    const v = view.current;
    if (!v || (shown.current && shown.current === activeRef.current)) return;
    const prev = docs.current.get(shown.current);
    if (prev && shown.current) prev.state = v.state;
    const next = docs.current.get(activeRef.current);
    if (next?.load === "ready" && next.state) {
      shown.current = next.path;
      v.setState(next.state);
      const head = next.state.selection.main.head;
      const line = next.state.doc.lineAt(head);
      setCursor({ line: line.number, col: head - line.from + 1 });
    } else {
      shown.current = "";
    }
  }, []);

  // focusEditor gives the editor focus a frame from now, once what was just
  // shown is laid out; unless something else in the window has taken it
  // meanwhile, such as the palette just opened.
  const focusEditor = useCallback(() => {
    requestAnimationFrame(() => {
      const now = document.activeElement;
      const elsewhere = now && now !== document.body && now !== rootEl.current && rootEl.current?.contains(now) && !host.current?.contains(now);
      if (!elsewhere) view.current?.focus();
    });
  }, []);

  // The Doc to focus once it has loaded: one opened before it was ready.
  const focusOnLoad = useRef("");
  const activate = useCallback(
    (p: string) => {
      activeRef.current = p;
      setActive(p);
      show();
      if (docs.current.get(p)?.load === "ready") focusEditor();
      else focusOnLoad.current = p;
    },
    [show, focusEditor],
  );

  const onUpdate = useRef<(u: ViewUpdate) => void>(() => {});
  onUpdate.current = (u: ViewUpdate) => {
    const d = docs.current.get(shown.current);
    if (!d) return;
    if (u.docChanged) {
      const dirty = !d.saved || !u.state.doc.eq(d.saved);
      if (dirty !== d.dirty) {
        d.dirty = dirty;
        bump();
      }
      scheduleAutoSave(d);
    }
    if (u.selectionSet || u.docChanged) {
      const head = u.state.selection.main.head;
      const line = u.state.doc.lineAt(head);
      setCursor({ line: line.number, col: head - line.from + 1 });
    }
  };

  // ------------------------------------------------------------ open, load

  // readOnlyReason says why a file opens read-only, or "". Whether it is
  // writable was checked on aosd as the Realm's user (M8.4).
  const readOnlyReason = (root: OpenWorkspaceResponse | null, info: FileInfo): string => {
    if (info.writable) return "";
    return root?.rootMode ? "Read-only: this is AOS’s own state, which Code doesn’t change even as root." : "Read-only in Standard Mode — aos can’t write this file.";
  };

  const loadBackups = async (d: Doc) => {
    if (!d.sensitive) return;
    const list = await files.listSensitiveBackups({ path: d.path }).catch(() => null);
    if (list && docs.current.get(d.path) === d) {
      d.backups = list.backups;
      bump();
    }
  };

  const load = useCallback(
    async (d: Doc, keepSelection?: number) => {
      try {
        const got = await readText(d.path);
        if (!docs.current.has(d.path) || docs.current.get(d.path) !== d) return;
        if (got.kind !== "text") {
          d.load = got.kind;
          d.note =
            got.kind === "binary"
              ? `${baseOf(d.path)} is not a text file.`
              : `${baseOf(d.path)} is ${formatSize(got.info.size, false)}. Code opens files up to ${formatSize(BigInt(MAX_TEXT), false)}.`;
          if (shown.current === d.path) shown.current = "";
          bump();
          return;
        }
        d.indent = detectIndent(got.text, d.path);
        d.crlf = got.text.includes("\r\n");
        d.first = firstLine(got.text);
        d.readOnly = readOnlyReason(wsRef.current, got.info);
        d.sensitive = got.info.sensitiveReason;
        const anchor = Math.min(keepSelection ?? 0, got.text.length);
        d.state = EditorState.create({ doc: got.text, selection: { anchor }, extensions: extensions(d) });
        d.saved = d.state.doc;
        d.stamp = stamp(got.info);
        d.dirty = false;
        d.disk = undefined;
        d.compare = undefined;
        d.load = "ready";
        d.note = "";
        if (activeRef.current === d.path) {
          if (shown.current === d.path) shown.current = ""; // replaced, not kept
          show();
        }
        if (d.reveal) {
          select(d, d.reveal);
          d.reveal = undefined;
        }
        bump();
        void loadBackups(d);
        void loadMarks();
        if (!d.lang) {
          const lang = await languageFor(d.path, d.first).catch(() => null);
          if (lang && docs.current.get(d.path) === d) {
            d.lang = lang;
            apply(d, { effects: compartments.current.lang.reconfigure(lang) });
          }
        }
      } catch (err) {
        if (docs.current.get(d.path) !== d) return;
        d.load = "error";
        d.note = friendlyError(err);
        bump();
      }
    },
    [bump, show],
  );

  const openDoc = useCallback(
    (p: string, focus = true) => {
      // Pictures, PDFs, audio and video open in Preview, as from Finder.
      const kind = fileKind(p);
      if (kind !== "text" && kind !== "other") return openFile(p);
      if (!docs.current.has(p)) {
        const d: Doc = {
          path: p,
          load: "loading",
          note: "",
          stamp: "",
          dirty: false,
          saving: false,
          error: "",
          readOnly: "",
          sensitive: "",
          backups: [],
          indent: { tabs: false, size: 2 },
          crlf: false,
          first: "",
          lang: null,
        };
        docs.current.set(p, d);
        setOrder((o) => [...o, p]);
        void load(d);
      }
      if (focus) activate(p);
    },
    [activate, load, openFile],
  );

  // select puts the cursor at a Reveal and scrolls to it.
  const select = (d: Doc, at: Reveal) => {
    const st = current(d);
    if (!st) return;
    if (at.pos !== undefined) {
      const pos = Math.min(at.pos, st.doc.length);
      apply(d, { selection: { anchor: pos }, effects: EditorView.scrollIntoView(pos, { y: "center" }) });
      return;
    }
    const line = st.doc.line(Math.min(Math.max(1, at.line ?? 1), st.doc.lines));
    let from = line.from;
    let to = line.from;
    if (at.find) {
      let i = line.text.indexOf(at.find);
      if (i < 0) i = line.text.toLowerCase().indexOf(at.find.toLowerCase());
      if (i >= 0) {
        from = line.from + i;
        to = from + at.find.length;
      }
    }
    apply(d, { selection: { anchor: from, head: to }, effects: EditorView.scrollIntoView(from, { y: "center" }) });
  };

  // reveal opens a file and, given a place, goes there: at once if it is
  // loaded, else once it is.
  const reveal = (p: string, at?: Reveal) => {
    openDoc(p);
    const d = docs.current.get(p);
    if (!d || !at) return;
    if (d.load === "ready") {
      select(d, at);
      focusEditor();
    } else d.reveal = at;
  };

  // ------------------------------------------------------------ navigation

  const back = useRef<Loc[]>([]);
  const forward = useRef<Loc[]>([]);

  // here is where the cursor is now, in the file in front.
  const here = (): Loc | null => {
    const d = docs.current.get(activeRef.current);
    const st = d && current(d);
    return d && st && d.load === "ready" ? { path: d.path, pos: st.selection.main.head } : null;
  };

  // jump goes somewhere, remembering where it came from for ⌃-.
  const jump = (p: string, at?: Reveal) => {
    const h = here();
    if (h) {
      back.current.push(h);
      if (back.current.length > HISTORY) back.current.shift();
      forward.current = [];
    }
    reveal(p, at);
  };

  // travel goes back (or forward) a place, opening its file again if it was
  // closed since.
  const travel = (from: Loc[], to: Loc[]) => {
    const loc = from.pop();
    if (!loc) return;
    const h = here();
    if (h) to.push(h);
    reveal(loc.path, { pos: loc.pos });
  };

  // The word at the cursor, in the file in front.
  const wordHere = (at?: number) => {
    const v = view.current;
    const d = docs.current.get(activeRef.current);
    if (!v || !d || shown.current !== d.path) return null;
    const w = wordAt(v.state, at ?? v.state.selection.main.head);
    return w ? { ...w, path: d.path } : null;
  };

  const say = (err: unknown) => {
    const e = ConnectError.from(err);
    setNotice(e.code === RpcCode.FailedPrecondition ? e.rawMessage : friendlyError(err));
  };

  // F12 and ⌘-click: one definition is gone to; several are offered.
  const goToDefinition = async (at?: number) => {
    const w = wordHere(at);
    const root = wsRef.current;
    if (!w || !root) return;
    try {
      const found = (await code.definition({ workspaceId: root.id, name: w.name, path: w.path })).symbols;
      if (found.length === 0) setNotice(`No definition found for ${w.name}.`);
      else if (found.length === 1) jump(found[0].path, { line: found[0].line, find: w.name });
      else setPalette({ initial: "", preset: { title: `${found.length} definitions of ${w.name}`, items: found.map((s) => symbolItem(s, root.root)) } });
    } catch (err) {
      say(err);
    }
  };
  const definitionRef = useRef(goToDefinition);
  definitionRef.current = goToDefinition;

  // ⇧F12: the name's whole-word uses in files of the same language.
  const findReferences = async () => {
    const w = wordHere();
    const root = wsRef.current;
    if (!w || !root) return;
    try {
      const r = await code.references({ workspaceId: root.id, name: w.name, path: w.path });
      const items: PaletteItem[] = r.files.flatMap((f) =>
        f.matches.map((m) => ({
          key: `${f.path}:${m.line}`,
          label: m.text,
          hits: m.ranges.flatMap((x) => Array.from({ length: x.end - x.start }, (_, i) => x.start + i)),
          detail: `${relative(f.path, root.root)}:${m.line}`,
          path: f.path,
          at: { line: m.line, find: w.name },
        })),
      );
      if (items.length === 0) return setNotice(`No references to ${w.name} found.`);
      const title = `${items.length}${r.truncated ? "+" : ""} ${items.length === 1 ? "reference" : "references"} to ${w.name}`;
      setPalette({ initial: "", preset: { title, items } });
    } catch (err) {
      say(err);
    }
  };

  // A notice goes by itself.
  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(""), 5000);
    return () => clearTimeout(t);
  }, [notice]);

  // ------------------------------------------------------------ save

  // save writes a Doc to disk. A Sensitive System File is saved only once the
  // person has confirmed, after its check (M8.5); aosd refuses it otherwise.
  const save = async (d: Doc, opts: { force?: boolean; confirmed?: boolean } = {}): Promise<boolean> => {
    const st = current(d);
    if (d.load !== "ready" || !st || d.readOnly || d.saving) return false;
    clearTimeout(d.timer);
    const bytes = new TextEncoder().encode(st.sliceDoc());
    if (bytes.length > MAX_TEXT) {
      d.error = `Code saves files up to ${formatSize(BigInt(MAX_TEXT), false)}.`;
      bump();
      return false;
    }
    if (d.sensitive && !opts.confirmed) {
      askToSave(d, bytes, opts);
      return false;
    }
    d.saving = true;
    d.error = "";
    bump();
    try {
      if (!opts.force && d.disk !== "deleted") {
        const now = await files.stat({ path: d.path }).then(
          (r) => stamp(r.info),
          (err) => {
            if (ConnectError.from(err).code === RpcCode.NotFound) return d.stamp; // deleted meanwhile: save it again
            throw err;
          },
        );
        if (now !== d.stamp) {
          d.disk = "changed";
          return false;
        }
      }
      ownWrites.current.set(d.path, Date.now());
      await files.write({ path: d.path, content: bytes, overwrite: true, confirmSensitive: Boolean(opts.confirmed) });
      d.stamp = stamp((await files.stat({ path: d.path })).info);
      d.saved = st.doc;
      markSaved(d, st.doc);
      d.disk = undefined;
      d.dirty = !current(d)?.doc.eq(st.doc);
      if (opts.confirmed) void loadBackups(d);
      return true;
    } catch (err) {
      // aosd knows a file as sensitive that this tab didn't (a new one in
      // /etc/sudoers.d, say): ask, then save again.
      const s = sensitiveOf(err);
      if (s && !opts.confirmed) {
        d.sensitive = s.reason;
        askToSave(d, bytes, opts);
        return false;
      }
      d.error = friendlyError(err);
      return false;
    } finally {
      d.saving = false;
      bump();
    }
  };

  // askToSave shows the Sensitive System File ask, with the file's check.
  const askToSave = (d: Doc, bytes: Uint8Array, opts: { force?: boolean }) => {
    const ask: SensitiveAsk = { path: d.path, reason: d.sensitive, action: "Save", check: "checking", keeps: true };
    const entry = {
      ask,
      confirm: () => {
        setSensitive(null);
        void save(d, { ...opts, confirmed: true });
      },
    };
    setSensitive(entry);
    void files.checkSensitive({ path: d.path, content: bytes }).then(
      (check) => setSensitive((cur) => (cur === entry || cur?.ask === ask ? { ...entry, ask: { ...ask, check } } : cur)),
      () => setSensitive((cur) => (cur?.ask === ask ? { ...entry, ask: { ...ask, check: undefined } } : cur)),
    );
  };

  // askSensitive asks before an Explorer change aosd refused, and retries it.
  const askSensitive = (s: SensitivePath, action: string, retry: () => Promise<void>) => {
    setSensitive({
      ask: { path: s.path, reason: s.reason, action, keeps: false },
      confirm: () => {
        setSensitive(null);
        void retry();
      },
    });
  };

  const restore = (d: Doc) => {
    const newest = d.backups[0];
    if (!newest) return;
    setSensitive({
      ask: { path: d.path, reason: d.sensitive, action: "Restore the previous version of", confirmLabel: "Restore", keeps: true },
      confirm: () => {
        setSensitive(null);
        void files.restoreSensitiveBackup({ path: d.path, id: newest.id }).then(
          () => load(d),
          (err) => {
            d.error = friendlyError(err);
            bump();
          },
        );
      },
    });
  };

  const scheduleAutoSave = (d: Doc) => {
    clearTimeout(d.timer);
    // Auto Save never saves a Sensitive System File (M8.5), nor a file being
    // compared (M8.10).
    if (!autoSaveRef.current || d.sensitive || d.compare) return;
    d.timer = setTimeout(() => {
      if (autoSaveRef.current && d.dirty && !d.disk && !d.readOnly && !d.sensitive && !d.compare && docs.current.get(d.path) === d) void save(d);
    }, AUTO_SAVE_MS);
  };

  const saveAll = async (paths: string[]) => {
    const results = await Promise.all(paths.map((p) => docs.current.get(p)).map((d) => (d ? save(d) : Promise.resolve(true))));
    return results.every(Boolean);
  };

  // ------------------------------------------------------------ compare

  // Compare with Saved, from the status bar or a mark in the gutter.
  const compareWithSaved = (line?: number) => {
    const d = docs.current.get(activeRef.current);
    if (!d || d.load !== "ready" || !d.saved || d.compare) return;
    clearTimeout(d.timer);
    d.compare = { kind: "saved", left: d.saved, line };
    bump();
  };
  const compareRef = useRef(compareWithSaved);
  compareRef.current = compareWithSaved;

  // compareWithDisk shows the version on disk beside a tab's unsaved edits.
  const compareWithDisk = async (d: Doc) => {
    try {
      const got = await readText(d.path);
      if (docs.current.get(d.path) !== d || d.load !== "ready") return;
      if (got.kind !== "text") {
        d.error = `the version on disk is ${got.kind === "binary" ? "not text" : "too big to compare"}.`;
        bump();
        return;
      }
      clearTimeout(d.timer);
      const text = EditorState.create({ doc: got.text, extensions: d.crlf ? EditorState.lineSeparator.of("\r\n") : [] }).doc;
      const st = current(d);
      d.compare = { kind: "disk", left: text, theirs: { text, stamp: stamp(got.info) }, line: st?.doc.lineAt(st.selection.main.head).number };
      bump();
    } catch (err) {
      d.error = friendlyError(err);
      bump();
    }
  };

  // endCompare closes a Doc's compare. A settled change on disk makes the
  // version on disk the one the tab's edits are against, so a save no longer
  // asks.
  const endCompare = (d: Doc, settled: boolean) => {
    const c = d.compare;
    if (!c) return;
    d.compare = undefined;
    if (settled && c.theirs) {
      d.saved = c.theirs.text;
      d.stamp = c.theirs.stamp;
      d.disk = undefined;
      markSaved(d, d.saved);
      d.dirty = !current(d)?.doc.eq(d.saved);
    }
    if (d.dirty) scheduleAutoSave(d);
    // Focus once the render that shows the editor again is done: a hidden
    // editor can't take it.
    focusOnLoad.current = d.path;
    bump();
  };

  // ------------------------------------------------------------ Change Review

  // Bumped when what the Agent changed may look different, so the list of
  // changed files is asked for again.
  const [reviewEpoch, setReviewEpoch] = useState(0);
  const reviewTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const reviewSoon = () => {
    clearTimeout(reviewTimer.current);
    reviewTimer.current = setTimeout(() => setReviewEpoch((n) => n + 1), 300);
  };
  // Files this window wrote, and when: a change the watch reports soon after
  // is its own, not a command's.
  const ownWrites = useRef(new Map<string, number>());
  // Files a command changed while one of this Workspace's Tasks ran, by Task,
  // with what the editor had (M8.11); null when the file wasn't open.
  const commandChanges = useRef(new Map<string, Map<string, Text | null>>());

  // noticeCommands records the files the watch saw change while a Task of
  // this Workspace ran. Those its Tools changed are left out of the list, as
  // aosd lists them itself.
  const noticeCommands = (ev: WorkspaceChanged) => {
    const root = wsRef.current?.root;
    const running = Object.values(useDesktop.getState().tasks)
      .filter((t) => t.workspace === root && t.state === TaskState.RUNNING)
      .sort((a, b) => Number((b.createdAt?.seconds ?? 0n) - (a.createdAt?.seconds ?? 0n)))[0];
    if (!running) return;
    const seen = commandChanges.current.get(running.id) ?? new Map<string, Text | null>();
    for (const c of ev.changes) {
      if (c.dir || c.kind === FileChange_Kind.RESCAN || Date.now() - (ownWrites.current.get(c.path) ?? 0) < 3000 || seen.has(c.path)) continue;
      const open = docs.current.get(c.path);
      seen.set(c.path, open?.load === "ready" && open.saved ? open.saved : null);
    }
    if (seen.size) commandChanges.current.set(running.id, seen);
  };

  // startReview opens a file the Agent changed as Change Review.
  const startReview = async (taskId: string, path: string, command = false) => {
    await loadMarks();
    if (!changesMod.current) return setNotice("Change Review could not load.");
    const open = docs.current.get(path);
    if (open?.dirty) return setNotice(`Save or undo your changes to ${baseOf(path)} before reviewing the Agent’s.`);
    let base: Text;
    let gone = false;
    if (command) {
      const had = commandChanges.current.get(taskId)?.get(path);
      if (!had) return;
      base = had;
    } else {
      try {
        const got = await taskApi.getBefore({ taskId, path });
        base = EditorState.create({ doc: new TextDecoder().decode(got.content) }).doc;
        gone = !got.change?.baseExists;
      } catch (err) {
        return say(err);
      }
    }
    openDoc(path);
    const d = docs.current.get(path);
    if (!d) return;
    d.review = { taskId, base, gone, command };
    // A Doc still loading takes it when its state is made.
    if (d.load === "ready") applyReview(d);
    bump();
  };

  const applyReview = (d: Doc) => {
    const c = compartments.current;
    apply(d, { effects: [c.review.reconfigure(reviewExt(d)), c.readOnly.reconfigure(editable(d))] });
  };

  const endReview = (d: Doc) => {
    if (!d.review) return;
    const r = d.review;
    d.review = undefined;
    if (r.command) commandChanges.current.get(r.taskId)?.delete(d.path);
    applyReview(d);
    bump();
    reviewSoon();
  };

  // hunks is how many changes are left in a Doc's review.
  const hunks = (d: Doc): number => {
    const st = current(d);
    return st && changesMod.current ? changesMod.current.hunksLeft(st) : 0;
  };

  // settled ends a review with nothing left to decide.
  const settled = (d: Doc) => {
    if (d.review && hunks(d) === 0) endReview(d);
    else bump();
    reviewSoon();
  };

  // keepChange keeps what is left of a Task's change to a file.
  const keepChange = async (taskId: string, path: string) => {
    await taskApi.reviewChange({ taskId, path, keepAll: true });
    const d = docs.current.get(path);
    if (d?.review?.taskId === taskId) endReview(d);
    reviewSoon();
  };

  // discardChange puts a file back as it is compared with, or to the Trash if
  // the Agent created it; a Sensitive System File asks first.
  const discardChange = async (taskId: string, path: string, confirmed = false): Promise<void> => {
    ownWrites.current.set(path, Date.now());
    try {
      await taskApi.discardChange({ taskId, path, confirmSensitive: confirmed });
    } catch (err) {
      const s = sensitiveOf(err);
      if (!s || confirmed) throw err;
      return new Promise((resolve, reject) =>
        askSensitive(s, "Discard the Agent’s changes to", () => discardChange(taskId, path, true).then(resolve, reject)),
      );
    }
    ownWrites.current.set(path, Date.now());
    const d = docs.current.get(path);
    if (d?.review) endReview(d);
    // A file the Agent created is gone, and so is its clean tab.
    if (d && !d.dirty && !(await files.stat({ path }).then(() => true, () => false))) drop(path);
    reviewSoon();
  };

  const reviewActs = useRef({
    kept: async (_d: Doc, _base: Text) => {},
    discarded: async (_d: Doc) => {},
    discardWhole: async (_d: Doc) => {},
  });
  reviewActs.current = {
    // A kept hunk: aosd compares the file with the base from now on.
    kept: async (d, base) => {
      const r = d.review;
      if (!r) return;
      r.base = base;
      r.gone = false;
      if (!r.command) {
        const text = base.sliceString(0, base.length, d.crlf ? "\r\n" : "\n");
        await taskApi.reviewChange({ taskId: r.taskId, path: d.path, base: new TextEncoder().encode(text) }).catch(say);
      }
      settled(d);
    },
    // A discarded hunk's old lines are back in the editor: saved as any edit
    // is, checked against the version on disk.
    discarded: async (d) => {
      await save(d);
      settled(d);
    },
    discardWhole: async (d) => {
      const r = d.review;
      if (r) await discardChange(r.taskId, d.path).catch(say);
    },
  };

  // The whole of a Doc's review at once, from its banner.
  const keepAllHere = (d: Doc) => {
    const r = d.review;
    if (!r) return;
    if (r.command) return endReview(d);
    void keepChange(r.taskId, d.path).catch(say);
  };
  const discardAllHere = (d: Doc) => {
    const r = d.review;
    if (!r) return;
    if (!r.command) return void discardChange(r.taskId, d.path).catch(say);
    const m = changesMod.current;
    const st = current(d);
    if (!m || !st) return;
    apply(d, { changes: m.replaceWith(st.doc, r.base.toString()), userEvent: "revert" });
    void save(d).then(() => endReview(d));
  };

  // What the list shows for a Task: the files a command changed while it ran.
  const commandsOf = (taskId: string): CommandChange[] =>
    [...(commandChanges.current.get(taskId) ?? new Map<string, Text | null>())].map(([path, had]) => ({ path, known: had !== null }));

  const changeList = (taskId: string) => (
    <ChangeList
      taskId={taskId}
      root={ws?.root ?? path}
      epoch={reviewEpoch}
      commands={commandsOf(taskId)}
      reviewing={d?.review?.taskId === taskId ? d.path : ""}
      onReview={(id, c: TaskFileChange) => void startReview(id, c.path)}
      onKeep={(id, c) => keepChange(id, c.path)}
      onDiscard={(id, c) => discardChange(id, c.path)}
      onCommand={(p, act) => {
        if (act === "review") void startReview(taskId, p, true);
        else {
          commandChanges.current.get(taskId)?.delete(p);
          const open = docs.current.get(p);
          if (open?.review?.command) endReview(open);
          setReviewEpoch((n) => n + 1);
        }
      }}
    />
  );

  // ------------------------------------------------------------ changes on disk

  // check looks at an open file again after the watch saw it change: a clean
  // tab takes the new version; an unsaved one is told.
  const check = async (d: Doc) => {
    if (d.saving || d.load === "loading") return;
    let now: string;
    try {
      now = stamp((await files.stat({ path: d.path })).info);
    } catch (err) {
      if (ConnectError.from(err).code === RpcCode.NotFound && docs.current.get(d.path) === d && d.disk !== "deleted") {
        d.disk = "deleted";
        bump();
      }
      return;
    }
    if (docs.current.get(d.path) !== d || d.saving) return;
    if (now === d.stamp) {
      if (d.disk === "deleted") {
        d.disk = undefined;
        bump();
      }
      return;
    }
    if (d.load !== "ready" || !d.dirty) {
      void load(d, current(d)?.selection.main.head);
      return;
    }
    if (d.disk !== "changed") {
      d.disk = "changed";
      bump();
    }
  };

  const onChanged = useRef<(ev: WorkspaceChanged) => void>(() => {});
  onChanged.current = (ev: WorkspaceChanged) => {
    if (ev.watch) setWatch(ev.watch);
    if (!ev.changes.length) return;
    noticeCommands(ev);
    reviewSoon();
    const dirs = new Set<string>();
    const touched = new Set<Doc>();
    let all = false;
    for (const c of ev.changes) {
      if (c.kind === FileChange_Kind.RESCAN) {
        all = true;
        continue;
      }
      if (c.kind !== FileChange_Kind.CHANGED) dirs.add(dirOf(c.path));
      for (const d of docs.current.values()) {
        if (d.path === c.path || (c.dir && c.kind !== FileChange_Kind.CHANGED && within(d.path, c.path))) touched.add(d);
      }
    }
    if (all) {
      relist("all");
      docs.current.forEach((d) => void check(d));
      return;
    }
    if (dirs.size) relist([...dirs]);
    touched.forEach((d) => void check(d));
  };

  // ------------------------------------------------------------ the Workspace

  const wsRef = useRef<OpenWorkspaceResponse | null>(null);
  useEffect(() => {
    const h = openWorkspace(path, winId, {
      onOpened: (resp, again) => {
        wsRef.current = resp;
        setWs(resp);
        setWsError("");
        setWinTitle(winId, baseOf(resp.root));
        if (again) {
          // Changes may have come and gone while the stream was down.
          relist("all");
          docs.current.forEach((d) => void check(d));
        }
      },
      onChanged: (ev) => onChanged.current(ev),
      onError: (err) => setWsError(friendlyError(err)),
    });
    handle.current = h;
    return () => {
      h.close();
      handle.current = null;
    };
  }, [path]);

  // A Doc opened before it had loaded takes focus once it is shown: after
  // the render that unhides the editor, since a hidden one cannot.
  // It takes focus only from nothing in particular: not from the palette or
  // an input opened while it loaded.
  useEffect(() => {
    const p = focusOnLoad.current;
    if (p && p === activeRef.current && shown.current === p && docs.current.get(p)?.load === "ready") {
      focusOnLoad.current = "";
      const now = document.activeElement;
      if (!now || now === document.body || now === rootEl.current) view.current?.focus();
    }
  });

  // The editor view lives as long as the Workspace is open.
  const ready = ws !== null;
  useEffect(() => {
    if (!ready || !host.current) return;
    const v = new EditorView({ parent: host.current });
    view.current = v;
    show();
    return () => {
      const d = docs.current.get(shown.current);
      if (d) d.state = v.state;
      shown.current = "";
      v.destroy();
      view.current = null;
    };
  }, [ready, show]);

  // The tabs the window had open come back with it.
  useEffect(() => {
    if (!ws || restored.current) return;
    restored.current = true;
    let tabs: string[] = [];
    try {
      tabs = JSON.parse(savedTabs || "[]") as string[];
    } catch {
      // A damaged list opens no tabs.
    }
    for (const p of tabs) if (within(p, ws.root)) openDoc(p, false);
    if (savedActive && docs.current.has(savedActive)) activate(savedActive);
    else if (tabs.length) activate(tabs[tabs.length - 1]);
  }, [ws]);

  // A file Finder asked this window to open.
  useEffect(() => {
    if (!ws || !openReq) return;
    openDoc(openReq);
    setWinState(winId, { open: "" });
  }, [ws, openReq, openDoc, setWinState, winId]);

  useEffect(() => {
    if (restored.current) setWinState(winId, { tabs: JSON.stringify(order), active });
  }, [order, active, setWinState, winId]);

  // aosd hears which files are unsaved, for the Realm switch (M8.13).
  const unsaved = order.filter((p) => docs.current.get(p)?.dirty);
  const unsavedKey = unsaved.join("\n");
  useEffect(() => {
    handle.current?.reportDirty(unsavedKey ? unsavedKey.split("\n") : []);
  }, [unsavedKey]);

  // Closing the window with unsaved files asks first.
  const unsavedRef = useRef(unsaved);
  unsavedRef.current = unsaved;
  useEffect(() => {
    setCloseGuard(winId, () => {
      if (unsavedRef.current.length === 0) return true;
      setAsk({ paths: unsavedRef.current, then: () => closeWindow(winId, true) });
      return false;
    });
    return () => setCloseGuard(winId, null);
  }, [winId, closeWindow]);

  // Auto Save turned on saves what is waiting.
  useEffect(() => {
    if (autoSave) docs.current.forEach((d) => d.dirty && scheduleAutoSave(d));
  }, [autoSave]);

  // ------------------------------------------------------------ tabs

  const drop = (p: string) => {
    const d = docs.current.get(p);
    if (d) clearTimeout(d.timer);
    docs.current.delete(p);
    if (shown.current === p) shown.current = "";
    // By the ref: a discard drops a tab after an await.
    const i = orderRef.current.indexOf(p);
    const rest = orderRef.current.filter((x) => x !== p);
    orderRef.current = rest;
    setOrder(rest);
    if (activeRef.current === p) activate(rest[Math.min(i, rest.length - 1)] ?? "");
  };

  const closeTab = (p: string) => {
    if (docs.current.get(p)?.dirty) setAsk({ paths: [p], then: () => drop(p) });
    else drop(p);
  };

  // A rename in the Explorer carries its open tabs along.
  const moved = (from: string, to: string) => {
    const renamed = new Map<string, string>();
    for (const p of order) if (within(p, from)) renamed.set(p, to + p.slice(from.length));
    if (!renamed.size) return;
    for (const [a, b] of renamed) {
      const d = docs.current.get(a)!;
      docs.current.delete(a);
      d.path = b;
      docs.current.set(b, d);
      if (shown.current === a) shown.current = b;
    }
    setOrder(order.map((p) => renamed.get(p) ?? p));
    const next = renamed.get(activeRef.current);
    if (next) {
      activeRef.current = next;
      setActive(next);
    }
  };

  // ------------------------------------------------------------ render

  const d = docs.current.get(active);

  // editorContext is where the user is working, for the Agent (M8.11).
  const editorContext = () => {
    const front = docs.current.get(activeRef.current);
    return { workspace: wsRef.current?.root ?? path, activeFile: front?.load === "ready" ? front.path : "", openFiles: order, selection: "", selectionStartLine: 0, selectionEndLine: 0 } as EditorContext;
  };
  const showAgent = (open = agentWidth === 0) => {
    setAgentWidth(open ? "340" : "0");
    if (!open) focusEditor();
  };
  // addToChat adds the selection, or the cursor's line, to the Agent's chat.
  const addToChat = () => {
    const v = view.current;
    const front = docs.current.get(activeRef.current);
    if (v && front && shown.current === front.path) {
      const sel = v.state.selection.main;
      const first = v.state.doc.lineAt(sel.from);
      // A selection ending at the start of a line doesn't take that line.
      const end = !sel.empty && v.state.doc.lineAt(sel.to).from === sel.to ? sel.to - 1 : sel.to;
      const last = v.state.doc.lineAt(Math.max(sel.from, end));
      const text = sel.empty ? first.text : v.state.sliceDoc(sel.from, sel.to);
      if (text.trim()) setAdded({ path: front.path, text, from: first.number, to: last.number });
    }
    if (agentWidth === 0) setAgentWidth("340");
    setAgentFocus((n) => n + 1);
  };

  const togglePanel = (open = !panelOpen) => {
    setPanel(open ? "1" : "");
    if (open) setPanelMounted(true);
    else focusEditor();
  };
  const side = Number(sideSetting) || 0;
  // showView shows a view in the side bar; its own button again hides the bar.
  const showView = (v: string, toggle = true) => {
    if (toggle && v === sideView && side > 0) setSide("0");
    else {
      setSideView(v);
      if (side === 0) setSide("240");
    }
  };
  const onKeyDown = (e: React.KeyboardEvent) => {
    const mod = e.metaKey || e.ctrlKey;
    const plain = !e.metaKey && !e.ctrlKey && !e.altKey;
    const take = () => {
      e.preventDefault();
      e.stopPropagation();
    };
    // ⌘P Quick Open, ⌘T a symbol anywhere, ⌘⇧O a symbol in this file, ⌃G a
    // line: one palette, in the mode its first character picks.
    if (mod && !e.altKey && !e.shiftKey && (e.code === "KeyP" || e.code === "KeyT")) {
      take();
      setPalette({ initial: e.code === "KeyT" ? "#" : "" });
      return;
    }
    if (mod && !e.altKey && e.shiftKey && e.code === "KeyO") {
      take();
      setPalette({ initial: "@" });
      return;
    }
    if (e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && e.code === "KeyG") {
      take();
      setPalette({ initial: ":" });
      return;
    }
    // F12 a definition, ⇧F12 references; ⌃- back and ⌃⇧- forward.
    if (e.key === "F12" && (plain || (e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey))) {
      take();
      void (e.shiftKey ? findReferences() : goToDefinition());
      return;
    }
    if (e.ctrlKey && !e.metaKey && !e.altKey && e.code === "Minus") {
      take();
      if (e.shiftKey) travel(forward.current, back.current);
      else travel(back.current, forward.current);
      return;
    }
    // ⌘L: the selected lines (or the cursor's line) go to the Agent's chat.
    if (mod && !e.altKey && !e.shiftKey && e.code === "KeyL") {
      take();
      addToChat();
      return;
    }
    // ⌘⇧F: Find in Files, with the selection if there is one.
    if (mod && !e.altKey && e.shiftKey && e.code === "KeyF") {
      e.preventDefault();
      e.stopPropagation();
      showView("search", false);
      const st = view.current?.state;
      const sel = st && !st.selection.main.empty ? st.sliceDoc(st.selection.main.from, st.selection.main.to) : "";
      setSearchReq((r) => ({ n: r.n + 1, text: sel && !sel.includes("\n") ? sel : undefined }));
      return;
    }
    if (mod && !e.altKey && e.shiftKey && e.code === "KeyE") {
      e.preventDefault();
      e.stopPropagation();
      showView("explorer", false);
      return;
    }
    // ⌃` shows or hides the Terminal panel, as in VS Code.
    if (e.ctrlKey && !e.metaKey && !e.altKey && e.code === "Backquote") {
      e.preventDefault();
      e.stopPropagation();
      togglePanel();
      return;
    }
    if (!(e.metaKey || e.ctrlKey) || e.code !== "KeyS") return;
    e.preventDefault();
    e.stopPropagation();
    if (e.altKey) void saveAll(order.filter((p) => docs.current.get(p)?.dirty));
    else if (d && (d.dirty || d.disk === "deleted")) void save(d);
  };

  if (!ws) {
    return (
      <div className="code code--empty">
        {wsError ? (
          <div className="preview__note">
            <p>{wsError}</p>
            <button className="finder__btn" onClick={onClose}>
              Open Another Folder
            </button>
          </div>
        ) : (
          <div className="preview__note">Opening {path}…</div>
        )}
      </div>
    );
  }

  return (
    // Focusable, so the window's shortcuts work after a click anywhere in it.
    <div className="code" ref={rootEl} tabIndex={-1} onKeyDownCapture={onKeyDown}>
      <div className="code__body">
        <div className="code__activity" role="tablist" aria-label="Views" aria-orientation="vertical">
          <button role="tab" aria-selected={side > 0 && sideView === "explorer"} aria-label="Explorer" title="Explorer (⌘⇧E)" className="code__activity-btn" onClick={() => showView("explorer")}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h8l5 5v13H6z M14 3v5h5" /></svg>
          </button>
          <button role="tab" aria-selected={side > 0 && sideView === "search"} aria-label="Search" title="Search (⌘⇧F)" className="code__activity-btn" onClick={() => showView("search")}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6" /><path d="M15 15l5.5 5.5" /></svg>
          </button>
          <button role="tab" aria-selected={side > 0 && sideView === "outline"} aria-label="Outline" title="Outline" className="code__activity-btn" onClick={() => showView("outline")}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 6h14M8 12h11M8 18h11M5 12h.01M5 18h.01" /></svg>
          </button>
        </div>
        <div className="code__side" style={{ width: side }} hidden={side === 0}>
          <div className="code__view" hidden={sideView !== "explorer"}>
            <Explorer root={ws.root} active={active} subscribe={subscribe} onOpen={openDoc} onMoved={moved} onError={setError} onSensitive={askSensitive} />
          </div>
          <div className="code__view" hidden={sideView !== "search"}>
            <SearchView workspaceId={ws.id} root={ws.root} request={searchReq} onReveal={(p, line, find) => jump(p, { line, find })} />
          </div>
          {/* Mounted only while shown: it asks aosd's index at each file switch. */}
          {sideView === "outline" && side > 0 && (
            <div className="code__view">
              <Outline
                workspaceId={ws.id}
                path={d?.load === "ready" ? d.path : ""}
                version={d?.stamp ?? ""}
                onReveal={(p, line, name) => jump(p, { line, find: name })}
              />
            </div>
          )}
        </div>
        <Splitter size={side} onSize={(w) => setSide(String(w))} min={160} max={520} label="Resize the Explorer" restore={240} />
        <div className="code__main">
          <div className="code__tabs" role="tablist" aria-label="Open files">
            {order.map((p) => {
              const doc = docs.current.get(p);
              return (
                <div
                  key={p}
                  role="tab"
                  aria-selected={p === active}
                  className={`code__tab${p === active ? " code__tab--on" : ""}${doc?.disk === "deleted" ? " code__tab--gone" : ""}`}
                  title={relative(p, ws.root)}
                  onClick={() => activate(p)}
                  onAuxClick={(e) => e.button === 1 && closeTab(p)}
                >
                  <span className="code__tab-name">{baseOf(p)}</span>
                  {doc?.readOnly && <span title="Read-only">🔒</span>}
                  <button
                    className={`code__tab-close${doc?.dirty ? " code__tab-close--dirty" : ""}`}
                    aria-label={doc?.dirty ? `${baseOf(p)} has unsaved changes; close` : `Close ${baseOf(p)}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      closeTab(p);
                    }}
                  />
                </div>
              );
            })}
          </div>

          {error && (
            <div className="code__banner code__banner--error" role="alert">
              <span>{error}</span>
              <button className="finder__btn" onClick={() => setError("")}>
                Dismiss
              </button>
            </div>
          )}
          {notice && (
            <div className="code__banner code__banner--info" role="status" aria-label="Notice">
              <span>{notice}</span>
            </div>
          )}
          {d?.review && (
            <div className="code__banner code__banner--review" role="region" aria-label="Change Review">
              <span>
                Reviewing {d.review.command ? "a command’s" : "the Agent’s"} changes to {baseOf(d.path)} — {hunks(d)} left.
              </span>
              <button className="finder__btn" onClick={() => keepAllHere(d)}>
                Keep All
              </button>
              <button className="finder__btn tasks__btn--danger" onClick={() => discardAllHere(d)}>
                Discard All
              </button>
              <button className="finder__btn" onClick={() => endReview(d)}>
                Done
              </button>
            </div>
          )}
          {d?.readOnly && <div className="code__banner code__banner--info">{d.readOnly}</div>}
          {d?.sensitive && (
            <div className="code__banner code__banner--warn" role="note" aria-label="Sensitive System File">
              <span>
                ⚠︎ <strong>Sensitive System File.</strong> {d.sensitive} Saving asks first; Auto Save leaves it alone.
              </span>
              {d.backups.length > 0 && (
                <button className="finder__btn" onClick={() => restore(d)}>
                  Restore Previous Version
                </button>
              )}
            </div>
          )}
          {d?.disk === "changed" && (
            <div className="code__banner code__banner--warn" role="alertdialog" aria-label="The file changed on disk">
              <span>{baseOf(d.path)} changed on disk, and you have unsaved changes.</span>
              {d.compare?.kind !== "disk" && (
                <button className="finder__btn finder__btn--on" onClick={() => void compareWithDisk(d)}>
                  Compare…
                </button>
              )}
              <button className="finder__btn" onClick={() => void load(d, current(d)?.selection.main.head)}>
                Take Theirs
              </button>
              <button className="finder__btn tasks__btn--danger" onClick={() => void save(d, { force: true })}>
                Overwrite with Mine
              </button>
            </div>
          )}
          {d?.disk === "deleted" && (
            <div className="code__banner code__banner--warn" role="status">
              <span>{baseOf(d.path)} was deleted on disk. Saving creates it again.</span>
              <button className="finder__btn" onClick={() => closeTab(d.path)}>
                Close Tab
              </button>
            </div>
          )}
          {d?.error && (
            <div className="code__banner code__banner--error" role="alert">
              <span>
                {baseOf(d.path)} was not saved: {d.error}
              </span>
            </div>
          )}

          <div className="code__editor" ref={host} hidden={d?.load !== "ready" || Boolean(d?.compare)} />
          {d?.load === "ready" && d.compare && (
            <Suspense fallback={<div className="preview__note">Loading…</div>}>
              <Compare
                key={`${d.path}\n${d.compare.kind}`}
                kind={d.compare.kind}
                name={baseOf(d.path)}
                left={d.compare.left}
                right={current(d)!.doc}
                line={d.compare.line}
                extensions={[theme, highlight, d.lang ?? [], d.crlf ? EditorState.lineSeparator.of("\r\n") : []]}
                onLeft={(text) => {
                  if (d.compare) d.compare.left = text;
                }}
                onRight={(changes: ChangeSet) => apply(d, { changes, userEvent: "input.compare" })}
                onDone={() => endCompare(d, true)}
                onClose={() => endCompare(d, false)}
              />
            </Suspense>
          )}
          {!d && <div className="preview__note">{order.length ? "" : "Open a file from the Explorer."}</div>}
          {d?.load === "loading" && <div className="preview__note">Loading…</div>}
          {d && (d.load === "binary" || d.load === "too-big" || d.load === "error") && (
            <div className="preview__note">
              <p>{d.note}</p>
              {d.load !== "error" && (
                <button className="finder__btn" onClick={() => revealInFinder(dirOf(d.path), d.path)}>
                  Show in Finder
                </button>
              )}
            </div>
          )}
          {panelMounted && (
            <>
              {panelOpen && (
                <Splitter
                  axis="y"
                  size={Number(panelHeight) || 220}
                  onSize={(h) => (h ? setPanelHeight(String(h)) : togglePanel(false))}
                  min={100}
                  max={900}
                  label="Resize the Terminal panel"
                  restore={220}
                />
              )}
              <div className="code__panel" style={{ height: Number(panelHeight) || 220 }} hidden={!panelOpen}>
                <Suspense fallback={<div className="preview__note">Loading…</div>}>
                  <TerminalPanel root={ws.root} visible={panelOpen} onHide={() => togglePanel(false)} />
                </Suspense>
              </div>
            </>
          )}
        </div>
        {agentWidth > 0 && (
          <>
            <Splitter side="after" size={agentWidth} onSize={(w) => (w > 0 ? setAgentWidth(String(w)) : showAgent(false))} min={260} max={720} label="Resize the Agent" restore={340} />
            <div className="code__agent" style={{ width: agentWidth }}>
              <CodeAgent
                root={ws.root}
                rootMode={ws.rootMode}
                editor={editorContext}
                added={added}
                onClearAdded={() => setAdded(null)}
                focus={agentFocus}
                onHide={() => showAgent(false)}
                footer={changeList}
              />
            </div>
          </>
        )}
      </div>

      <StatusBar
        ws={ws}
        watch={watch}
        doc={d}
        cursor={cursor}
        autoSave={autoSave}
        onAutoSave={(on) => setAutoSave(on ? "on" : "off")}
        panelOpen={panelOpen}
        onPanel={() => togglePanel()}
        agentOpen={agentWidth > 0}
        onAgent={() => showAgent()}
        onCompare={() => compareWithSaved()}
      />

      {palette && (
        <QuickOpen
          workspaceId={ws.id}
          root={ws.root}
          active={d?.load === "ready" ? d.path : ""}
          initial={palette.initial}
          preset={palette.preset}
          onOpen={jump}
          onClose={() => {
            setPalette(null);
            focusEditor();
          }}
        />
      )}

      {sensitive && (
        <SensitiveConfirm
          ask={sensitive.ask}
          onConfirm={() => {
            sensitive.confirm();
            view.current?.focus();
          }}
          onCancel={() => {
            setSensitive(null);
            view.current?.focus(); // so ⌘S reaches the editor again
          }}
        />
      )}

      {ask && (
        <SaveDialog
          names={ask.paths.map(baseOf)}
          onCancel={() => {
            setAsk(null);
            view.current?.focus();
          }}
          onDiscard={() => {
            setAsk(null);
            ask.then();
          }}
          onSave={async () => {
            setAsk(null);
            if (await saveAll(ask.paths)) ask.then();
          }}
        />
      )}
    </div>
  );
}

function StatusBar({
  ws,
  watch,
  doc,
  cursor,
  autoSave,
  onAutoSave,
  panelOpen,
  onPanel,
  agentOpen,
  onAgent,
  onCompare,
}: {
  ws: OpenWorkspaceResponse;
  watch?: WorkspaceWatch;
  doc?: Doc;
  cursor: { line: number; col: number };
  autoSave: boolean;
  onAutoSave: (on: boolean) => void;
  panelOpen: boolean;
  onPanel: () => void;
  agentOpen: boolean;
  onAgent: () => void;
  onCompare: () => void;
}) {
  const ready = doc?.load === "ready";
  return (
    <div className="code__status" role="status" aria-label="Status bar">
      <span className={`code__realm${ws.rootMode ? " code__realm--root" : ""}`}>{ws.rootMode ? "Root Mode · root" : "Standard Mode · aos"}</span>
      <WatchState watch={watch} />
      <button className="code__status-btn" aria-pressed={panelOpen} onClick={onPanel} title="Show or hide the Terminal panel (⌃`)">
        ⌨ Terminal
      </button>
      <button className="code__status-btn" aria-pressed={agentOpen} onClick={onAgent} title="Show or hide the Agent (⌘L adds the selection)">
        ✦ Agent
      </button>
      <span className="code__status-gap" />
      {ready && (
        <>
          {doc.dirty && !doc.compare && (
            <button className="code__status-btn" onClick={onCompare} title="Show what changed since the file was saved">
              Compare with Saved
            </button>
          )}
          <span>
            Ln {cursor.line}, Col {cursor.col}
          </span>
          <span>{doc.indent.tabs ? `Tab Size: ${doc.indent.size}` : `Spaces: ${doc.indent.size}`}</span>
          <span>UTF-8</span>
          <span>{doc.crlf ? "CRLF" : "LF"}</span>
          <span>{languageName(doc.path, doc.first)}</span>
        </>
      )}
      <button className="code__status-btn" aria-pressed={autoSave} onClick={() => onAutoSave(!autoSave)} title="Save each file a second after you stop typing">
        Auto Save: {autoSave ? "On" : "Off"}
      </button>
    </div>
  );
}

function WatchState({ watch }: { watch?: WorkspaceWatch }) {
  if (!watch) return null;
  switch (watch.state) {
    case WorkspaceWatch_State.LIVE:
      return <span title="Changes on disk show at once">● Live</span>;
    case WorkspaceWatch_State.POLLING:
      return <span title={watch.reason}>◌ Checking every 2 s</span>;
    case WorkspaceWatch_State.STOPPED:
      return <span className="code__watch-off" title={watch.reason}>○ Not watching: {watch.reason}</span>;
  }
  return null;
}

// SaveDialog asks what to do with unsaved files before they close.
function SaveDialog({ names, onSave, onDiscard, onCancel }: { names: string[]; onSave: () => void; onDiscard: () => void; onCancel: () => void }) {
  const title = names.length === 1 ? `Save the changes to ${names[0]}?` : `Save the changes to ${names.length} files?`;
  return (
    <div
      className="confirm__scrim"
      onClick={onCancel}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onCancel();
        }
      }}
    >
      <div className="confirm" role="alertdialog" aria-label={title} aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3 className="confirm__title">{title}</h3>
        <p className="confirm__message">Your changes are lost if you don’t save them.</p>
        {names.length > 1 && (
          <ul className="code__save-list">
            {names.map((n, i) => (
              <li key={`${n}-${i}`}>{n}</li>
            ))}
          </ul>
        )}
        <div className="confirm__actions">
          <button className="finder__btn tasks__btn--danger" onClick={onDiscard}>
            Don’t Save
          </button>
          <button className="finder__btn" onClick={onCancel}>
            Cancel
          </button>
          <button className="finder__btn finder__btn--on" autoFocus onClick={onSave}>
            {names.length === 1 ? "Save" : "Save All"}
          </button>
        </div>
      </div>
    </div>
  );
}

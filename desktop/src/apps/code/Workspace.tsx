// One Code window's Workspace (PLAN.md M8.3): the Explorer, the tabs, the
// editor and the status bar over a folder aosd watches for this tab.
//
// Each open file is a Doc. The window has one CodeMirror view and swaps each
// Doc's EditorState in and out of it as tabs change, so undo history and the
// selection stay with the file. A Doc remembers the text last opened or saved
// and the version of the file on disk it came from (its stamp): a save checks
// the stamp first and asks rather than overwrite a change made meanwhile, and
// the Workspace's watch reloads a clean tab when its file changes on disk.
import { indentUnit } from "@codemirror/language";
import { Compartment, EditorState, type Extension, type Text, type TransactionSpec } from "@codemirror/state";
import { EditorView, type ViewUpdate } from "@codemirror/view";
import { Code as RpcCode, ConnectError } from "@connectrpc/connect";
import { basicSetup } from "codemirror";
import { useCallback, useContext, useEffect, useRef, useState } from "react";

import { files } from "../../api/client";
import { friendlyError } from "../../api/error";
import { sensitiveOf } from "../../api/sensitive";
import { openWorkspace, type WorkspaceHandle } from "../../api/workspaces";
import { detectIndent, MAX_TEXT, readText, stamp, type Indent } from "../../editor/document";
import { languageFor, languageName } from "../../editor/languages";
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
} from "../../gen/aos/v1/services_pb";
import { setCloseGuard } from "../../shell/closeGuard";
import { WinContext, useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { SensitiveConfirm, type SensitiveAsk } from "../../ui/SensitiveConfirm";
import { Splitter } from "../../ui/Splitter";
import { fileKind } from "../filetypes";
import { formatSize } from "../finder/fs";
import { Explorer, type Relist } from "./Explorer";
import { baseOf, dirOf, relative, within } from "./paths";

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
  lang: Extension | null;
  timer?: ReturnType<typeof setTimeout>;
}

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

  const [ws, setWs] = useState<OpenWorkspaceResponse | null>(null);
  const [wsError, setWsError] = useState("");
  const [watch, setWatch] = useState<WorkspaceWatch | undefined>();
  const [order, setOrder] = useState<string[]>([]);
  const [active, setActive] = useState("");
  const [cursor, setCursor] = useState({ line: 1, col: 1 });
  const [error, setError] = useState("");
  const [ask, setAsk] = useState<Ask | null>(null);
  const [sensitive, setSensitive] = useState<{ ask: SensitiveAsk; confirm: () => void } | null>(null);
  const [, setTick] = useState(0);
  const bump = useCallback(() => setTick((n) => n + 1), []);

  const docs = useRef(new Map<string, Doc>());
  const view = useRef<EditorView | null>(null);
  const host = useRef<HTMLDivElement>(null);
  const shown = useRef(""); // the Doc whose state is in the view
  const activeRef = useRef("");
  const handle = useRef<WorkspaceHandle | null>(null);
  const relisters = useRef(new Set<Relist>());
  const compartments = useRef({ lang: new Compartment(), indent: new Compartment(), readOnly: new Compartment() });
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

  const extensions = (d: Doc): Extension[] => {
    const c = compartments.current;
    const ro = Boolean(d.readOnly);
    return [
      basicSetup,
      theme,
      highlight,
      c.lang.of(d.lang ?? []),
      c.indent.of([indentUnit.of(d.indent.tabs ? "\t" : " ".repeat(d.indent.size)), EditorState.tabSize.of(d.indent.tabs ? d.indent.size : 4)]),
      c.readOnly.of([EditorState.readOnly.of(ro), EditorView.editable.of(!ro)]),
      d.crlf ? EditorState.lineSeparator.of("\r\n") : [],
      EditorView.updateListener.of((u) => onUpdate.current(u)),
    ];
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

  const activate = useCallback(
    (p: string) => {
      activeRef.current = p;
      setActive(p);
      show();
      if (docs.current.get(p)?.load === "ready") requestAnimationFrame(() => view.current?.focus());
    },
    [show],
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
        d.readOnly = readOnlyReason(wsRef.current, got.info);
        d.sensitive = got.info.sensitiveReason;
        const anchor = Math.min(keepSelection ?? 0, got.text.length);
        d.state = EditorState.create({ doc: got.text, selection: { anchor }, extensions: extensions(d) });
        d.saved = d.state.doc;
        d.stamp = stamp(got.info);
        d.dirty = false;
        d.disk = undefined;
        d.load = "ready";
        d.note = "";
        if (activeRef.current === d.path) {
          if (shown.current === d.path) shown.current = ""; // replaced, not kept
          show();
        }
        bump();
        void loadBackups(d);
        if (!d.lang) {
          const lang = await languageFor(baseOf(d.path)).catch(() => null);
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
      await files.write({ path: d.path, content: bytes, overwrite: true, confirmSensitive: Boolean(opts.confirmed) });
      d.stamp = stamp((await files.stat({ path: d.path })).info);
      d.saved = st.doc;
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
    // Auto Save never saves a Sensitive System File (M8.5).
    if (!autoSaveRef.current || d.sensitive) return;
    d.timer = setTimeout(() => {
      if (autoSaveRef.current && d.dirty && !d.disk && !d.readOnly && !d.sensitive && docs.current.get(d.path) === d) void save(d);
    }, AUTO_SAVE_MS);
  };

  const saveAll = async (paths: string[]) => {
    const results = await Promise.all(paths.map((p) => docs.current.get(p)).map((d) => (d ? save(d) : Promise.resolve(true))));
    return results.every(Boolean);
  };

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
    const h = openWorkspace(path, {
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
    const i = order.indexOf(p);
    const rest = order.filter((x) => x !== p);
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
  const onKeyDown = (e: React.KeyboardEvent) => {
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

  const side = Number(sideSetting) || 0;
  return (
    <div className="code" onKeyDownCapture={onKeyDown}>
      <div className="code__body">
        <div className="code__side" style={{ width: side }} hidden={side === 0}>
          <Explorer root={ws.root} active={active} subscribe={subscribe} onOpen={openDoc} onMoved={moved} onError={setError} onSensitive={askSensitive} />
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

          <div className="code__editor" ref={host} hidden={d?.load !== "ready"} />
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
        </div>
      </div>

      <StatusBar ws={ws} watch={watch} doc={d} cursor={cursor} autoSave={autoSave} onAutoSave={(on) => setAutoSave(on ? "on" : "off")} />

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
}: {
  ws: OpenWorkspaceResponse;
  watch?: WorkspaceWatch;
  doc?: Doc;
  cursor: { line: number; col: number };
  autoSave: boolean;
  onAutoSave: (on: boolean) => void;
}) {
  const ready = doc?.load === "ready";
  return (
    <div className="code__status" role="status" aria-label="Status bar">
      <span className={`code__realm${ws.rootMode ? " code__realm--root" : ""}`}>{ws.rootMode ? "Root Mode · root" : "Standard Mode · aos"}</span>
      <WatchState watch={watch} />
      <span className="code__status-gap" />
      {ready && (
        <>
          <span>
            Ln {cursor.line}, Col {cursor.col}
          </span>
          <span>{doc.indent.tabs ? `Tab Size: ${doc.indent.size}` : `Spaces: ${doc.indent.size}`}</span>
          <span>UTF-8</span>
          <span>{doc.crlf ? "CRLF" : "LF"}</span>
          <span>{languageName(doc.path)}</span>
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

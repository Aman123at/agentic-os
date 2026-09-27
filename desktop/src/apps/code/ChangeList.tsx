// The files a Task changed, under its chat in Code's Agent sidebar (PLAN.md
// M8.12): "3 files changed — Review", each with Keep and Discard, and Keep
// All / Discard All for the Task. A file opens as Change Review in the editor;
// what is decided is kept by aosd, so what is left survives a reload. Files a
// command changed while the Task ran are listed too (M8.11): aosd kept no
// before-image of them, so they are compared with what the editor last had.
import { useEffect, useState } from "react";

import { tasks as taskApi } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { TaskFileChange } from "../../gen/aos/v1/services_pb";
import { useDesktop } from "../../store";
import { relative } from "./paths";

// CommandChange is a file a command changed while a Task ran.
export interface CommandChange {
  path: string;
  // What the editor had, when the file was open; else nothing to compare.
  known: boolean;
}

export interface ChangeListProps {
  taskId: string;
  root: string;
  // Bumped when files may have changed, so the list is asked for again.
  epoch: number;
  commands: CommandChange[];
  reviewing: string;
  onReview: (taskId: string, change: TaskFileChange) => void;
  onKeep: (taskId: string, change: TaskFileChange) => Promise<void>;
  onDiscard: (taskId: string, change: TaskFileChange) => Promise<void>;
  onCommand: (path: string, act: "review" | "dismiss") => void;
}

// kind names what the Task did to a file.
function kind(c: TaskFileChange): { mark: string; text: string } {
  if (c.folder) return { mark: "▣", text: c.tool === "delete" ? "folder deleted" : "folder moved" };
  if (c.tool === "delete" || (c.tool === "move" && c.existed && c.baseExists)) return { mark: "D", text: c.tool === "move" ? "moved away" : "deleted" };
  if (!c.existed) return { mark: "A", text: "added" };
  return { mark: "M", text: c.whole ? "changed (compared whole)" : "changed" };
}

// reviewable: it can open as Change Review, hunk by hunk.
export function reviewable(c: TaskFileChange): boolean {
  const k = kind(c).mark;
  return (k === "M" || k === "A") && !c.whole && !c.folder;
}

export function ChangeList({ taskId, root, epoch, commands, reviewing, onReview, onKeep, onDiscard, onCommand }: ChangeListProps) {
  const [changes, setChanges] = useState<TaskFileChange[]>([]);
  const [open, setOpen] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [asked, setAsked] = useState(0);
  // The Task's progress: each step it takes may change files.
  const updated = useDesktop((s) => s.tasks[taskId]?.updatedAt?.seconds ?? 0n);
  const steps = useDesktop((s) => (s.feeds[taskId] ?? []).map((st) => st.toolCall?.status ?? 0).join(","));

  useEffect(() => {
    let live = true;
    taskApi.listChanges({ taskId }).then(
      (r) => live && setChanges(r.changes),
      () => live && setChanges([]),
    );
    return () => {
      live = false;
    };
  }, [taskId, epoch, updated, steps, asked]);

  const pending = changes.filter((c) => c.pending);
  const listed = new Set(changes.map((c) => c.path));
  const cmds = commands.filter((c) => !listed.has(c.path));
  const n = pending.length + cmds.length;
  if (changes.length === 0 && cmds.length === 0) return null;

  const act = async (f: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await f();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
      setAsked((x) => x + 1);
    }
  };

  return (
    <div className="code-changes" role="region" aria-label="Changed files">
      <div className="code-changes__head">
        {n === 0 ? (
          <span className="code-changes__done">✓ {changes.length === 1 ? "The changed file is" : `All ${changes.length} changed files are`} reviewed</span>
        ) : (
          <>
            <button className="code-changes__toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
              {open ? "▾" : "▸"} {n} {n === 1 ? "file" : "files"} changed — Review
            </button>
            {pending.length > 0 && (
              <>
                <button className="finder__btn" disabled={busy} onClick={() => void act(async () => { for (const c of pending) await onKeep(taskId, c); })}>
                  Keep All
                </button>
                <button className="finder__btn tasks__btn--danger" disabled={busy} onClick={() => void act(async () => { for (const c of pending) if (!c.folder) await onDiscard(taskId, c); })}>
                  Discard All
                </button>
              </>
            )}
          </>
        )}
      </div>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      {open && n > 0 && (
        <ul className="code-changes__list">
          {pending.map((c) => {
            const k = kind(c);
            const canReview = reviewable(c);
            return (
              <li key={c.path} className={`code-changes__row${reviewing === c.path ? " code-changes__row--on" : ""}`}>
                <span className={`code-changes__mark code-changes__mark--${k.mark}`} title={k.text} aria-label={k.text}>
                  {k.mark}
                </span>
                <button className="code-changes__name" title={canReview ? `Review ${relative(c.path, root)}` : k.text} disabled={!canReview} onClick={() => onReview(taskId, c)}>
                  {relative(c.path, root)}
                </button>
                <button className="code__icon-btn" title="Keep" aria-label={`Keep the changes to ${relative(c.path, root)}`} disabled={busy} onClick={() => void act(() => onKeep(taskId, c))}>
                  ✓
                </button>
                {!c.folder && (
                  <button className="code__icon-btn" title={k.mark === "D" ? "Restore" : "Discard"} aria-label={`Discard the changes to ${relative(c.path, root)}`} disabled={busy} onClick={() => void act(() => onDiscard(taskId, c))}>
                    ↶
                  </button>
                )}
              </li>
            );
          })}
          {cmds.map((c) => (
            <li key={c.path} className={`code-changes__row${reviewing === c.path ? " code-changes__row--on" : ""}`}>
              <span className="code-changes__mark code-changes__mark--C" title="changed by a command" aria-label="changed by a command">
                C
              </span>
              <button
                className="code-changes__name"
                title={c.known ? `Review ${relative(c.path, root)}, against what the editor had` : "Changed by a command; it wasn't open, so there is nothing to compare with"}
                disabled={!c.known}
                onClick={() => onCommand(c.path, "review")}
              >
                {relative(c.path, root)} <span className="code-changes__by">by a command</span>
              </button>
              <button className="code__icon-btn" title="Dismiss" aria-label={`Dismiss ${relative(c.path, root)}`} onClick={() => onCommand(c.path, "dismiss")}>
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

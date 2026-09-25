// Code's Agent sidebar (PLAN.md M8.11): the same Agent as the Agent app — its
// chat, Follow-ups and inline Approvals — started from a Workspace. A chat here
// is an ordinary Task in the Realm's history (the Agent app shows it too); the
// sidebar lists this Workspace's Tasks, and tells the Agent where the user is
// working: the Workspace, the file in front, the files open and any text ⌘L
// added from the editor.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { friendlyError } from "../../api/error";
import type { EditorContext } from "../../gen/aos/v1/services_pb";
import { useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { TaskFeed } from "../agent/TaskDetail";
import { millis, stateLabel, taskTitle } from "../agent/format";
import { baseOf, relative } from "./paths";

// Added is text ⌘L added to the chat from a file.
export interface Added {
  path: string;
  text: string;
  from: number; // its first and last line
  to: number;
}

export interface CodeAgentProps {
  root: string;
  rootMode: boolean;
  // Where the user is working now: the Workspace, the file in front and the
  // files open.
  editor: () => EditorContext;
  added: Added | null;
  onClearAdded: () => void;
  // Bumped when ⌘L wants the composer focused.
  focus: number;
  onHide: () => void;
  // What goes above the composer for the Task shown: its changed files (M8.12).
  footer?: (taskId: string) => ReactNode;
}

export function CodeAgent({ root, rootMode, editor, added, onClearAdded, focus, onHide, footer }: CodeAgentProps) {
  const [taskId, setTaskId] = useWinState("agentTask", "");
  const tasks = useDesktop((s) => s.tasks);
  const feeds = useDesktop((s) => s.feeds);
  const loadTasks = useDesktop((s) => s.loadTasks);
  const watchTask = useDesktop((s) => s.watchTask);
  const composer = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    void loadTasks(100, root);
  }, [loadTasks, root]);

  // The Task shown keeps its feed live.
  useEffect(() => (taskId ? watchTask(taskId) : undefined), [taskId, watchTask]);

  useEffect(() => {
    if (focus) composer.current?.focus();
  }, [focus]);

  const list = useMemo(
    () =>
      Object.values(tasks)
        .filter((t) => t.workspace === root)
        .sort((a, b) => millis(b.createdAt) - millis(a.createdAt)),
    [tasks, root],
  );
  // A Task deleted elsewhere leaves a new chat.
  const task = taskId ? tasks[taskId] : undefined;
  useEffect(() => {
    if (taskId && !task && list.length && !list.some((t) => t.id === taskId)) setTaskId("");
  }, [taskId, task, list, setTaskId]);

  // context is what goes with a message: where the user is, and what ⌘L added.
  const context = (): EditorContext => {
    const e = editor();
    if (!added) return e;
    return { ...e, activeFile: added.path, selection: added.text, selectionStartLine: added.from, selectionEndLine: added.to };
  };

  const chips = (
    <div className="code-agent__chips">
      {editor().activeFile && !added && (
        <span className="code-agent__chip" title="The Agent is told which file is in front">
          📄 {baseOf(editor().activeFile)}
        </span>
      )}
      {added && (
        <span className="code-agent__chip code-agent__chip--sel" title={added.text}>
          ✂︎ {relative(added.path, root)}:{added.from === added.to ? added.from : `${added.from}–${added.to}`}
          <button className="code-agent__chip-x" aria-label="Remove the added selection" onClick={onClearAdded}>
            ×
          </button>
        </span>
      )}
    </div>
  );

  return (
    <div className="code-agent" role="complementary" aria-label="Agent">
      <div className="code-agent__bar">
        <select className="code-agent__pick" aria-label="Chat" value={task ? taskId : ""} onChange={(e) => setTaskId(e.target.value)}>
          <option value="">New chat</option>
          {list.map((t) => (
            <option key={t.id} value={t.id}>
              {taskTitle(t)} — {stateLabel(t.state).text}
            </option>
          ))}
        </select>
        <button className="code__icon-btn" title="New chat" aria-label="New chat" onClick={() => setTaskId("")}>
          ＋
        </button>
        <button className="code__icon-btn" title="Hide the Agent" aria-label="Hide the Agent" onClick={onHide}>
          ×
        </button>
      </div>
      {task ? (
        <TaskFeed
          taskId={task.id}
          steps={feeds[task.id] ?? []}
          editor={context}
          above={
            <>
              {footer?.(task.id)}
              {chips}
            </>
          }
          onSent={onClearAdded}
          composer={composer}
        />
      ) : (
        <NewChat root={root} rootMode={rootMode} context={context} chips={chips} composer={composer} onStarted={(id) => {
          onClearAdded();
          setTaskId(id);
        }} />
      )}
    </div>
  );
}

function NewChat({
  root,
  rootMode,
  context,
  chips,
  composer,
  onStarted,
}: {
  root: string;
  rootMode: boolean;
  context: () => EditorContext;
  chips: ReactNode;
  composer: React.RefObject<HTMLTextAreaElement | null>;
  onStarted: (taskId: string) => void;
}) {
  const createTask = useDesktop((s) => s.createTask);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const send = async () => {
    const prompt = text.trim();
    if (!prompt || busy) return;
    setBusy(true);
    setError("");
    try {
      const task = await createTask(prompt, undefined, { editor: context() });
      setText("");
      if (task) onStarted(task.id);
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="code-agent__new">
      <div className="code-agent__intro">
        <p>
          Ask the Agent to explain or change <strong>{baseOf(root)}</strong>. It works as {rootMode ? "root" : "aos"}, with the same rights as in the Agent app,
          and can build and test what it changes.
        </p>
        <p className="code-agent__hint">⌘L adds the selected lines to the chat.</p>
      </div>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      <footer className="tasks__foot">
        {chips}
        <textarea
          ref={composer}
          className="tasks__reply"
          aria-label="Message the Agent"
          placeholder="What should the Agent do? (⌘Enter to send)"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              e.stopPropagation();
              void send();
            }
          }}
        />
        <div className="tasks__actions">
          <button className="tasks__btn tasks__btn--go" disabled={busy || !text.trim()} onClick={() => void send()}>
            Send
          </button>
        </div>
      </footer>
    </div>
  );
}

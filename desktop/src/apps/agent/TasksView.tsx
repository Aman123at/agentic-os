// The Agent app's Tasks view: the Task history, newest first and filterable by
// state, beside the Task picked in it. The pick is kept with the window; the
// store's live step feed follows it.
import { useEffect, useMemo, useState } from "react";

import { friendlyError } from "../../api/error";
import { useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { Confirm } from "../../ui/Confirm";
import { ContextMenu, MenuItem } from "../../ui/ContextMenu";
import { VirtualList } from "../../ui/VirtualList";
import NewTask from "./NewTask";
import TaskDetail from "./TaskDetail";
import { TASK_FILTERS, isLive, matchesFilter, millis, shortCost, stateLabel, taskTitle, when } from "./format";

// How much Task history the list loads; boot loads only the newest 50.
const HISTORY = 500;
const ROW_HEIGHT = 52;

export default function TasksView() {
  const [taskId, setTaskId] = useWinState("task", "");
  const [filter, setFilter] = useWinState("filter", "all");
  const tasks = useDesktop((s) => s.tasks);
  const approvals = useDesktop((s) => s.approvals);
  const selectTask = useDesktop((s) => s.selectTask);
  const loadTasks = useDesktop((s) => s.loadTasks);
  const deleteTask = useDesktop((s) => s.deleteTask);
  const [composing, setComposing] = useState(false);
  // The row right-click menu, and the Task awaiting a delete confirmation.
  const [menu, setMenu] = useState<{ x: number; y: number; id: string } | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    void loadTasks(HISTORY);
  }, [loadTasks]);

  useEffect(() => {
    selectTask(taskId);
  }, [taskId, selectTask]);

  // A click anywhere, or losing focus, dismisses the row menu.
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
    };
  }, [menu]);

  const confirmTask = confirming ? tasks[confirming] : undefined;
  async function doDelete(id: string) {
    setConfirming(null);
    setError("");
    try {
      await deleteTask(id);
    } catch (err) {
      setError(friendlyError(err));
    }
  }

  const waiting = useMemo(() => new Set(Object.values(approvals).map((a) => a.taskId)), [approvals]);
  const list = useMemo(
    () =>
      Object.values(tasks)
        .filter((t) => matchesFilter(t.state, filter))
        .sort((a, b) => millis(b.createdAt) - millis(a.createdAt)),
    [tasks, filter],
  );

  return (
    <div className="agent__tasks">
      <div className="agent__listpane">
        <div className="agent__toolbar">
          <select className="agent__select" aria-label="Filter by state" value={filter} onChange={(e) => setFilter(e.target.value)}>
            {TASK_FILTERS.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name}
              </option>
            ))}
          </select>
          <span className="agent__count">{list.length}</span>
          <button className="tasks__btn tasks__btn--go agent__new" onClick={() => setComposing(true)}>
            ＋ New Task
          </button>
        </div>
        {error && (
          <div className="tasks__error" role="alert">
            {error}
          </div>
        )}
        {list.length === 0 ? (
          <div className="agent__empty">No Tasks here.</div>
        ) : (
          <VirtualList
            className="agent__list"
            items={list}
            rowHeight={ROW_HEIGHT}
            renderRow={(t, style) => {
              const st = stateLabel(t.state);
              return (
                <button
                  key={t.id}
                  style={style}
                  className={`agent__row${t.id === taskId ? " agent__row--on" : ""}`}
                  aria-current={t.id === taskId ? "true" : undefined}
                  onClick={() => setTaskId(t.id)}
                  onContextMenu={(e) => {
                    e.preventDefault();
                    setMenu({ x: e.clientX, y: e.clientY, id: t.id });
                  }}
                  title={t.prompt}
                >
                  <span className="agent__rowtitle">
                    {waiting.has(t.id) && <span aria-label="Needs your approval">🔐 </span>}
                    {taskTitle(t)}
                  </span>
                  <span className="agent__rowmeta">
                    <span className={`agent__dot tasks__state--${st.mod}`} />
                    {st.text} · {when(t.createdAt)}
                    <span className="agent__rowcost">{shortCost(t.usage)}</span>
                  </span>
                </button>
              );
            }}
          />
        )}
      </div>
      <div className="agent__detail">
        <TaskDetail />
      </div>
      {menu && tasks[menu.id] && (
        <ContextMenu x={menu.x} y={menu.y}>
          <MenuItem
            label="Delete Task…"
            danger
            disabled={isLive(tasks[menu.id].state)}
            title={isLive(tasks[menu.id].state) ? "Cancel the Task before deleting it" : undefined}
            onClick={() => setConfirming(menu.id)}
          />
        </ContextMenu>
      )}
      {confirmTask && (
        <Confirm
          title="Delete Task?"
          message={`Delete “${taskTitle(confirmTask)}” and its steps, Approvals and grants. The Audit Log keeps its record. This cannot be undone.`}
          confirmLabel="Delete"
          danger
          onConfirm={() => void doDelete(confirmTask.id)}
          onCancel={() => setConfirming(null)}
        />
      )}
      {composing && <NewTask onClose={() => setComposing(false)} />}
    </div>
  );
}

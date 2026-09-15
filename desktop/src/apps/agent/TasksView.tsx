// The Agent app's Tasks view: the Task history, newest first and filterable by
// state, beside the Task picked in it. The pick is kept with the window; the
// store's live step feed follows it.
import { useEffect, useMemo } from "react";

import { useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { VirtualList } from "../../ui/VirtualList";
import TaskDetail from "./TaskDetail";
import { TASK_FILTERS, matchesFilter, millis, shortCost, stateLabel, taskTitle, when } from "./format";

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

  useEffect(() => {
    void loadTasks(HISTORY);
  }, [loadTasks]);

  useEffect(() => {
    selectTask(taskId);
  }, [taskId, selectTask]);

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
        </div>
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
    </div>
  );
}

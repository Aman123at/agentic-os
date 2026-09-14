-- M1: Tasks, steps, Approvals, grants, Audit Log, Protected Paths, settings, Memory proposals.
-- Times are Unix milliseconds.

CREATE TABLE tasks (
  id                   TEXT PRIMARY KEY,
  title                TEXT NOT NULL,
  prompt               TEXT NOT NULL,
  state                INTEGER NOT NULL,
  autonomy             INTEGER NOT NULL,
  interactive          INTEGER NOT NULL,
  summary              TEXT NOT NULL DEFAULT '',
  model                TEXT NOT NULL DEFAULT '',
  awaiting_approval_id TEXT NOT NULL DEFAULT '',
  awaiting_question    TEXT NOT NULL DEFAULT '',
  input_tokens         INTEGER NOT NULL DEFAULT 0,
  cached_input_tokens  INTEGER NOT NULL DEFAULT 0,
  output_tokens        INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens     INTEGER NOT NULL DEFAULT 0,
  created_at           INTEGER NOT NULL,
  updated_at           INTEGER NOT NULL,
  finished_at          INTEGER
);
CREATE INDEX tasks_created ON tasks (created_at);

CREATE TABLE task_steps (
  id             TEXT PRIMARY KEY,
  task_id        TEXT NOT NULL REFERENCES tasks (id),
  seq            INTEGER NOT NULL,
  kind           INTEGER NOT NULL,
  text           TEXT NOT NULL DEFAULT '',
  call_id        TEXT NOT NULL DEFAULT '',
  tool           TEXT NOT NULL DEFAULT '',
  arguments_json TEXT NOT NULL DEFAULT '',
  status         INTEGER NOT NULL DEFAULT 0,
  result         TEXT NOT NULL DEFAULT '',
  output_ref     TEXT NOT NULL DEFAULT '',
  -- Model output items (JSON) this step came from, to rebuild the transcript.
  items_json     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  started_at     INTEGER,
  finished_at    INTEGER,
  UNIQUE (task_id, seq)
);

CREATE TABLE approvals (
  id                   TEXT PRIMARY KEY,
  task_id              TEXT NOT NULL REFERENCES tasks (id),
  step_id              TEXT NOT NULL,
  tool                 TEXT NOT NULL,
  summary              TEXT NOT NULL,
  reasons_json         TEXT NOT NULL,
  protected_paths_json TEXT NOT NULL,
  grantable            INTEGER NOT NULL,
  decision             INTEGER NOT NULL DEFAULT 0,
  decided_by           TEXT NOT NULL DEFAULT '',
  created_at           INTEGER NOT NULL,
  decided_at           INTEGER
);
CREATE INDEX approvals_pending ON approvals (decision, task_id);

-- "Allow for the rest of this Task": same Tool, same folder.
CREATE TABLE grants (
  task_id    TEXT NOT NULL REFERENCES tasks (id),
  tool       TEXT NOT NULL,
  folder     TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (task_id, tool, folder)
);

CREATE TABLE audit_log (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  time           INTEGER NOT NULL,
  task_id        TEXT NOT NULL DEFAULT '',
  step_id        TEXT NOT NULL DEFAULT '',
  tool           TEXT NOT NULL DEFAULT '',
  arguments_json TEXT NOT NULL DEFAULT '',
  decision       TEXT NOT NULL DEFAULT '',
  decided_by     TEXT NOT NULL DEFAULT '',
  result_summary TEXT NOT NULL DEFAULT '',
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  actor          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_task ON audit_log (task_id, id);
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN SELECT RAISE(ABORT, 'the Audit Log is append-only'); END;
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN SELECT RAISE(ABORT, 'the Audit Log is append-only'); END;

-- Paths the user locked (🔒, aos protect); defaults live in code.
CREATE TABLE protected_paths (
  path       TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- Memory: `remember` only proposes; the user accepts.
CREATE TABLE memories (
  id         TEXT PRIMARY KEY,
  text       TEXT NOT NULL,
  status     TEXT NOT NULL,
  task_id    TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

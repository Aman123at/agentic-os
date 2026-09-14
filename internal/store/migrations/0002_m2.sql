-- M2: the Install Ledger, Checkpoints, Services and usage per day. Tasks gain
-- the Checkpoint taken before their first software change, the reason they
-- await the user, and an estimated cost. Times are Unix milliseconds.

ALTER TABLE tasks ADD COLUMN awaiting_kind INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN checkpoint_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN cost_known INTEGER NOT NULL DEFAULT 1;

-- Model usage per day (in the Machine's time zone) and model.
CREATE TABLE usage (
  day                 TEXT NOT NULL,
  model               TEXT NOT NULL,
  input_tokens        INTEGER NOT NULL DEFAULT 0,
  cached_input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens       INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens    INTEGER NOT NULL DEFAULT 0,
  cost_usd            REAL NOT NULL DEFAULT 0,
  cost_known          INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (day, model)
);

-- The Install Ledger (ADR-0003, PLAN.md §11): every operation with root
-- authority, with the packages, /etc files and Services it changed. The states
-- are JSON; '' means absent. Append-only, like the Audit Log.
CREATE TABLE ledger_ops (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  time    INTEGER NOT NULL,
  task_id TEXT NOT NULL DEFAULT '',
  action  TEXT NOT NULL,
  summary TEXT NOT NULL,
  actor   TEXT NOT NULL DEFAULT ''
);
CREATE TABLE ledger_entries (
  op_id        INTEGER NOT NULL REFERENCES ledger_ops (id),
  kind         TEXT NOT NULL,
  manager      TEXT NOT NULL DEFAULT '',
  name         TEXT NOT NULL,
  before_state TEXT NOT NULL DEFAULT '',
  after_state  TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (op_id, kind, manager, name)
);
CREATE INDEX ledger_entries_item ON ledger_entries (kind, manager, name, op_id);
CREATE TRIGGER ledger_ops_no_update BEFORE UPDATE ON ledger_ops
BEGIN SELECT RAISE(ABORT, 'the Install Ledger is append-only'); END;
CREATE TRIGGER ledger_ops_no_delete BEFORE DELETE ON ledger_ops
BEGIN SELECT RAISE(ABORT, 'the Install Ledger is append-only'); END;
CREATE TRIGGER ledger_entries_no_update BEFORE UPDATE ON ledger_entries
BEGIN SELECT RAISE(ABORT, 'the Install Ledger is append-only'); END;
CREATE TRIGGER ledger_entries_no_delete BEFORE DELETE ON ledger_entries
BEGIN SELECT RAISE(ABORT, 'the Install Ledger is append-only'); END;

-- A Checkpoint is a Ledger position: a Restore undoes every operation after it.
CREATE TABLE checkpoints (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  ledger_id  INTEGER NOT NULL,
  task_id    TEXT NOT NULL DEFAULT '',
  automatic  INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);

-- Services aosd supervises (ADR-0005). definition is JSON.
CREATE TABLE services (
  name       TEXT PRIMARY KEY,
  definition TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

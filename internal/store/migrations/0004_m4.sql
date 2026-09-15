-- M4: notifications are kept until the user dismisses them, so the Notification
-- Center shows them again after a reload (PLAN.md §4.3, §14).

CREATE TABLE notifications (
  id           TEXT PRIMARY KEY,
  title        TEXT NOT NULL,
  body         TEXT NOT NULL DEFAULT '',
  task_id      TEXT NOT NULL DEFAULT '',
  memory_id    TEXT NOT NULL DEFAULT '',
  port         INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  dismissed_at INTEGER
);

CREATE INDEX notifications_open ON notifications (created_at) WHERE dismissed_at IS NULL;

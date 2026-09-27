-- M8.11: Tasks started from Code belong to a Workspace folder, and the files a
-- Task changes with its Tools keep their before-images.
ALTER TABLE tasks ADD COLUMN workspace TEXT NOT NULL DEFAULT '';
CREATE INDEX tasks_workspace ON tasks (workspace, created_at);

-- One row per file per Task, written the first time the Task changes it. blob
-- names the before-image in the Realm's outputs/<task>/before/, '' when none
-- was kept (the file did not exist, was a folder, or was over 64 MiB). base is
-- what Change Review compares the file with (M8.12): 0 the before-image, 1 the
-- before-image with the hunks the user kept, in <blob name>.base, 2 "does not
-- exist".
CREATE TABLE task_file_changes (
  task_id    TEXT NOT NULL REFERENCES tasks (id),
  path       TEXT NOT NULL,
  tool       TEXT NOT NULL,
  existed    INTEGER NOT NULL,
  folder     INTEGER NOT NULL,
  size       INTEGER NOT NULL,
  blob       TEXT NOT NULL,
  whole      INTEGER NOT NULL,
  changed_at INTEGER NOT NULL,
  base       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (task_id, path)
);

-- M3: the Desktop's saved layout (PLAN.md §4.3, §13). One opaque JSON blob the
-- Desktop owns — window positions, theme, wallpaper — restored on reload so a
-- browser refresh brings the windows back. Single-user, so a single row.

CREATE TABLE desktop_state (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  state    TEXT NOT NULL DEFAULT '',
  saved_at INTEGER NOT NULL DEFAULT 0
);

---
status: accepted
---

# Code: the Agent writes, then you review; navigation from ctags, not language servers

M8 adds **Code**, a VS Code–like editor on the Desktop over a **Workspace** (a
folder of the Machine), with the Agent in a sidebar. Two choices shape most of
it and are hard to walk back once built on.

## 1. The Agent writes to disk; the user reviews afterwards

When the Agent in Code changes files, it uses its ordinary Tools (`write_file`,
`edit_file`, …) with its ordinary Realm rights and Approvals. The change lands
on disk at once. The **first** time a Task changes a file, AOS keeps the file's
previous content — its *before-image*, or "did not exist" — in the Realm's
`outputs/<task>/before/`, indexed in a `task_file_changes` table. **Change
Review** then shows each changed file as a diff against its before-image with
*Keep* / *Discard* on every hunk. *Discard* is the user's own write of the old
lines back, through `FileService.Write`.

### Consequences

- The Agent can build and test the change it just made with `run_command`, the
  same Task, the same turn. A change it cannot run is a change it cannot check.
- *Discard* always has something to go back to, even after the window closed or
  the Desktop reloaded, because the before-image lives on the server, not in the
  editor.
- Files a command changes (a formatter, `sed -i`) have no before-image from a
  Tool; the Workspace watch notices them and lists them as *changed by a
  command*, reviewable against what the editor last had if the file was open.
  This gap is accepted rather than snapshotting the Workspace before every
  command.
- Before-images are Realm-scoped like the rest of a Task's record, and go when
  the Task is deleted or Root Mode history is cleared.

### Considered options

- **Staged edits that reach disk only when kept:** rejected. The Agent would
  reason about files that don't exist yet, could not run or test them, and every
  Tool that reads files would need to see the staged overlay. It also splits
  "what's on disk" from "what the Agent believes", which is exactly the
  confusion Change Review is meant to remove.
- **Snapshot the whole Workspace (or `git stash`) when a Task starts:** rejected;
  cost grows with the Workspace, it fails outside Git, and it would catch the
  user's own edits made meanwhile as if they were the Agent's.

## 2. Code intelligence from `universal-ctags` and `ripgrep`, not language servers

Go to Definition, Find References, symbols in a file or Workspace, and
completion from Workspace names come from `universal-ctags` (definitions, ~100
languages) and `ripgrep` (text and references), both run **as the Realm's user**
through the confined files worker, behind one `CodeService`. Both are small apt
packages added to the baseline software and `install.sh`.

### Consequences

- One mechanism serves every language, with no per-language install and nothing
  long-running per Workspace beyond an in-memory index.
- Navigation is **by name, not by type**: two methods called `Run` are both
  offered, nearest first. The docs say so.
- A Machine without `ctags` still edits; navigation says how to get it.
- `CodeService` is the seam a language-server bridge (type-aware completion,
  rename, hover, diagnostics) plugs into later, without changing the editor's
  calls — a candidate M9.

### Considered options

- **Language servers (LSP) in M8:** deferred, not rejected. Each language needs
  its own server installed on the Machine and kept running per Workspace, run
  under the Realm's rights and bridged over a socket — roughly doubling M8
  before the editor itself is usable.
- **Index in the browser from Lezer parse trees:** rejected; it would read the
  whole Workspace over the API on every open, and only languages with a Lezer
  grammar would navigate.

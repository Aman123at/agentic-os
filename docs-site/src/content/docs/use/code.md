---
title: Code
description: A code editor on the Desktop, with the Agent beside it. What you can edit in each mode, how the Agent's changes are reviewed, and what its navigation can't do.
---

**Code** is a code editor in the Desktop, over a folder of the Machine — its
**Workspace**. Open one from the Dock (it asks for a folder, your home by default),
from *Open in Code* on a folder or file in Finder's right-click menu, or from Spotlight
(*Open Folder in Code…*). Each window has one Workspace, and you can have several
windows open.

A window has an Explorer, tabs, a status bar, a Terminal panel at the bottom and the
Agent in a sidebar on the right. Files up to 4 MiB open for editing; images, PDFs and
other binary files open in Preview.

| Keys | What they do |
|---|---|
| ⌘S | Save |
| ⌘⌥S | Save all |
| ⌘P | Quick Open: a file by name. Start with `:` for a line (⌃G), `#` for a symbol anywhere (⌘T), `@` for a symbol in this file (⌘⇧O) |
| ⌘⇧F | Find in Files |
| ⌘⇧E | The Explorer |
| F12 | Go to Definition (⌘-click works too) |
| ⇧F12 | References |
| ⌃- | Back |
| ⌃⇧- | Forward |
| ⌃\` | Show or hide the Terminal panel |
| ⌘L | Add the selection to the Agent's chat |

⌘ is Ctrl on Linux and Windows. ⌘T is the browser's New Tab, which a page can't always
take, so `#` in ⌘P is the sure way to it.

## What you can edit

Code acts with the rights of the mode the Machine is in, exactly as Finder and the
Terminal do. It adds no way past them.

- **In Standard Mode** it reads and writes as the `aos` user. A file `aos` can't write,
  such as `/etc/hosts`, opens **read-only**, with a lock on its tab and a banner saying
  why. **Auto Save** saves each file a second after you stop typing, and is on by
  default.
- **In [Root Mode](/use/root-mode/)** it reads and writes as root, so every file is
  writable except Agentic OS's own state (`/var/lib/aos`, `/etc/aos`, and its own binary
  and service), which opens read-only. Auto Save is **off** by default, so nothing
  reaches a system file before you press ⌘S.

You can switch Auto Save on or off for each window in the status bar. If a file changes
on disk while it's open, a tab with no unsaved changes reloads. A tab with unsaved
changes offers **Compare…** instead, where you take the disk's version or keep yours
hunk by hunk. Lines you've changed since saving are marked in the gutter, and
**Compare with Saved** in the status bar shows them side by side.

The Terminal panel opens its shells in the Workspace folder, as `aos` or as root like
the Terminal app. Hiding the panel keeps them running, and closing the window ends
them.

## You can't switch modes while Code is open

Switching between Standard and Root Mode restarts Agentic OS and reloads the Desktop.
Unsaved edits would be lost, and editors would be left holding files opened with the
other mode's rights. So the switch is refused, both ways, while **any** Code window is
open, even with everything saved. The System pane says *"Code is open. Please save your
files and close Code before switching mode."*, lists each open folder and its unsaved
files, and has a **Show** button for each Code window in this browser tab.
[`aos root on|off`](/reference/root/) prints the same list and exits with an error.

The refusal covers Code open in any browser tab. Close those windows, then switch.
*Restart AOS* and *Clear Root Mode history* are not blocked, but their confirmations
list your unsaved files first.

## The Agent, and Change Review

The sidebar (**✦ Agent** in the status bar, or ⌘L) is the same Agent as the Agent app,
with the same Tools and the same rights. It isn't given any more to work in Code. A chat
you start here is an ordinary Task: it appears in the Agent app too, and the sidebar
lists the Tasks started from this Workspace. The Agent is told which folder it's in,
which file you're looking at, your selection and the files you have open.

**The Agent writes its changes to disk, then you review them.** It doesn't stage them
and wait for you. That way it can build and test its own change with the commands it
runs. The first time a Task changes a file, Agentic OS keeps the file's previous
content, or notes that it didn't exist. Nothing the Agent does can take that away from
you.

While and after a Task runs, the sidebar shows *"3 files changed — Review"*. Open a file
from that list to see the Agent's change as a diff against what was there before.

- **Keep** or **Discard** each hunk. **Keep All** and **Discard All** work per file and
  for the whole Task.
- **Discard** puts the old lines back, and a file the Agent created goes to the Trash. It
  writes with your rights and is checked against the file on disk. A Sensitive System
  File still asks.
- A file under review is read-only until you've decided. If you had it open with no
  unsaved changes, it updates live as the Agent writes. If it has unsaved changes, you
  get a compare instead of being overwritten.
- What you haven't decided is kept by Agentic OS. Close the window or reload, and it's
  still there until you decide or delete the Task.
- Files the Agent's *commands* changed, rather than its file Tools, are listed as
  *changed by a command*. They can be compared only with what the editor had, so only if
  the file was open, and that list lasts only while the window is open.
- A file over 4 MiB, or one that isn't text, is kept or discarded whole. So is a folder,
  which can only be kept; restore it from the Trash if you need it back.

## Sensitive System Files (Root Mode)

As root, a single wrong line in some files can lock everyone out of `sudo` or SSH, cut
the Machine off the network, or stop it booting. Agentic OS calls these **Sensitive
System Files**. They are:

- accounts and login: `/etc/passwd`, `shadow`, `group`, `gshadow`, `sudoers`,
  `sudoers.d/`, `pam.d/`, `security/`
- booting: `/etc/fstab`, `crypttab`, `default/grub`, `/boot/`, plus the kernel's
  `sysctl` and `modprobe.d` settings
- the network and SSH: `/etc/hosts`, `hostname`, `resolv.conf`, `netplan/`, `network/`,
  `ssh/`, and every `~/.ssh/authorized_keys`
- `/etc/systemd/`, `/etc/apt/`, `/etc/crontab` and `/etc/cron.d/`
- the system's programs and libraries: `/bin`, `/sbin`, `/lib*`, `/usr/bin`, `/usr/sbin`,
  `/usr/lib`
- Agentic OS itself

In Root Mode, when you work with one of these files:

- **Opening it** shows a yellow banner saying what can go wrong.
- **Saving it** asks first. A red dialog names the file and the risk, and says Agentic OS
  can't undo the change. **Auto Save never saves one.**
- **A check runs first, where one exists:** `visudo -c` for sudoers, `sshd -t` for
  `sshd_config`, `findmnt --verify` for `fstab`. If the check fails, you see its output
  and the button reads *Save anyway (check failed)*.
- **A way back:** the version you replace is kept, the last five per file, and **Restore
  previous version** in the banner puts one back.
- **Agents ask too.** An Agent's change to one of these files is a risky action. On
  `confirm-risky` it waits for your approval, and an "allow for the rest of this Task"
  never covers it.

Agentic OS enforces this itself: its API refuses a change to one of these files that
hasn't been confirmed, so Code, Finder and TextEdit all ask. None of this applies in
Standard Mode, because `aos` can't write these files anyway.

## What navigation can't do

Go to Definition, references and symbols come from
[universal-ctags](https://github.com/universal-ctags/ctags) and
[ripgrep](https://github.com/BurntSushi/ripgrep). They work across files in about a
hundred languages with nothing to set up for each language. The catch is that they go
by **name**, not by type.

- **Two things with the same name are both offered.** F12 on `Run` lists every `Run`,
  nearest first. When the name is defined in the file's own language, only those
  definitions are offered.
- **References are a text search.** They find whole-word, case-sensitive uses in files of
  the same language, up to 1,000 lines. That includes comments and strings, and a
  different symbol with the same name.
- **There's no rename, no hover types, no errors as you type**, and completion offers
  the language's keywords and the Workspace's symbol names only.
- **The first question in a Workspace builds its index**, so the first F12 in a large
  project takes a moment. After that, only the files you change are indexed again.
- **Past 20,000 files**, only the folders of the files you ask from are indexed, and Code
  tells you so.
- **Without universal-ctags**, Go to Definition and symbols say *install
  universal-ctags* instead: `sudo apt install universal-ctags`. `install.sh` installs it
  where apt is available. Without ripgrep, Find in Files uses Agentic OS's own, slower
  search, which gives the same results.

Quick Open and Find in Files follow the Workspace's `.gitignore` files and skip `.git`,
`node_modules` and binary files. Quick Open lists up to 100,000 files, and Find in Files
stops at 2,000 matching lines. A folder of more than 8,000 folders, or one past the
Machine's file-watching limit, is checked every two seconds rather than watched, and
Code says so.

Also not in Code yet: a Git view (Code's diffs are against the saved file and the
Agent's before-images, not Git), Replace in Files, split editors, debugging, extensions,
and more than one folder in a window.

---
title: Root Mode
description: A second way to run the whole Machine, in which Agents, the Terminal and Finder act as root. What it does, what it keeps apart, and what it cannot protect.
---

Normally every Agent, the Terminal and Finder act as the `aos` user: no `sudo`,
Protected Paths need an Approval, and the paths you lock stay locked. **Root Mode**
is a second way to run the whole Machine in which all of that acts as **root** —
Agents can run anything that needs `sudo`, every file and folder is unlocked, and
Protected Paths are not enforced.

It is a separate **Realm**, not a setting you toggle inside a session. The Machine
runs one Realm at a time, chosen at start from `root_mode:` in `config.yml`, so every
switch **restarts the daemon and reloads the Desktop**. When Root Mode is running you
will know it: a red **ROOT** badge in the menu bar, a banner across the Agent app, and
a `root@…#` prompt in the Terminal.

## What it does

- **Agents run as root.** `sudo` is unnecessary — they *are* root. They can install
  packages, edit anything under `/etc`, and manage system services directly.
- **Nothing is locked.** The [Protected Paths](/reference/protect/) list, your own
  🔒 locks, the `.env` guard and the "don't discard uncommitted work" git guard are all
  switched off. Finder shows your locks as *not enforced in Root Mode*.
- **The Terminal and Finder are root too.** `id` prints `uid=0(root)`, `/root` is your
  home, and every folder opens.

Risky actions still follow your Autonomy level. On `confirm-risky` an Agent is still
stopped before an `rm -rf` or a package removal — being root removes the *permission*
wall, not the *are-you-sure* one. Set Autonomy to `auto` if you want none.

## What it keeps apart

Root Mode has its own history, kept in its own database, separate from your normal
(**Standard**) Realm:

- Its Tasks (chats), [Audit Log](/reference/audit/), Memory, Notifications, Services,
  window layout and Browser profile are all its own.
- **In Standard Mode you see none of it, and the other way round.** The daemon only ever
  serves the Realm that is running, so the Desktop and the CLI *cannot* show the other
  Realm's history — there is no query that reaches across.
- Even a root Agent is kept out of Agentic OS's own state (`/var/lib/aos`, `/etc/aos`) by
  the kernel, so it does not stumble into Standard's history or your API key.

It is *like* incognito, but not the same: Root Mode's history is kept across restarts.
When you want it gone, [`aos root clear`](/reference/root/) (or *Clear Root Mode history*
in the System pane) deletes that database and its command outputs, and the next Root start
begins empty.

## What it cannot do

**Root Mode is a privacy boundary between the two histories — not a security boundary
against a root Agent.** An Agent running as root can, if it really tries, get around
anything on the machine, Agentic OS included: it can reach its own Approvals, undo its own
confinement, and touch files outside any Realm. The separation keeps the two histories from
*mixing*; it does not contain a root process that is determined to escape.

And it does not undo what happened. **What root did to the server is real and stays.** A
package it installed is still installed, a file it wrote under `/etc` is still there, a
service it started keeps running. Clearing Root Mode's history erases Agentic OS's *record*
of the work, not the work. There is no Checkpoint or Replay that walks it back — Checkpoints
do not cover what root does outside them.

## Turning it on

Flip **Start in Root Mode** in the System pane, or run `aos root on`. You will read a
warning of exactly the consequences above, confirm that you understand, and enter your
Desktop password. The Machine restarts into Root Mode. The switch is refused while a Task is
running and names the Task; cancel it and try again.

Because [the Desktop password is already root on a native install](/start/password-is-root/),
Root Mode does not hand an attacker anything they could not already reach — it hands *your
Agents* the same authority, all the time, with the guardrails off. Turn it on for a job that
needs it, and turn it back off when the job is done: leaving Root Mode stops every service it
started and returns the Machine to its locked, `aos`-user self.

If this is more standing authority than you want in one place, the
[Docker Compose install](/start/compose/) keeps Root Mode's blast radius inside a container
instead of your server.

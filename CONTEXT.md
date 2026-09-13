# Agentic OS (AOS)

A personal, containerised Ubuntu environment where AI Agents carry out everyday computer work (moving files, installing software, downloading things) on the user's behalf, viewable from a terminal or a macOS-style Desktop in the browser.

## Language

### The environment

**Agentic OS (AOS)**:
The product name. Not an operating system in the kernel sense; it is a layer on top of Ubuntu.
_Avoid_: the OS (when you mean the Machine)

**Machine**:
The Ubuntu environment that AOS runs and that Agents act upon, including its filesystem, processes and installed software.
_Avoid_: OS, container, VM, box

**Host**:
The user's own computer on which the Machine runs, which may be macOS, Windows or Linux.
_Avoid_: local machine, laptop, Mac

**Service**:
A named, long-running program on the Machine that AOS keeps running and brings back after a restart.
_Avoid_: daemon, background job, systemd unit

**Machine Profile**:
A concise, always-current description of the Machine (its Ubuntu version, installed software, key folders and Services) given to every Agent.
_Avoid_: system info, environment summary

**Mode**:
The way a user interacts with AOS: `cli` (terminal only) or `ui` (terminal plus the Desktop).
_Avoid_: flavour, variant, edition

**Desktop**:
The macOS-style graphical interface for the Machine, built as a web app and viewed in the browser. It runs only its own built-in apps, not Linux GUI programs.
_Avoid_: GUI, VNC, remote desktop, UI (when you mean the Desktop specifically)

**Shared Folder**:
A folder visible both on the user's host computer and inside the Machine, used to move files in and out.
_Avoid_: mount, volume, bind

### Agents and work

**Task**:
A single request from the user that AOS works to fulfil, such as "install ffmpeg and convert these videos".
_Avoid_: job, prompt, command, request

**Follow-up**:
A further instruction given to a Task after it has finished, continuing it with its previous history rather than starting a new Task.
_Avoid_: new task, reply, continuation

**Awaiting User**:
The state of a Task that cannot proceed until the user acts: deciding an Approval, answering a question, or choosing what to do after Retries are exhausted or a Cost Limit is reached.
_Avoid_: blocked, paused, awaiting approval

**Retry**:
Another attempt by an Agent at a step that just failed, or a repeat of an identical Tool invocation; too many in a row make the Task Awaiting User.
_Avoid_: attempt, loop

**Interrupted**:
The state of a Task whose work was cut short by AOS stopping, as opposed to one the user Cancelled; it can be Resumed on request.
_Avoid_: crashed, aborted

**Agent**:
The AI loop that works on exactly one Task, choosing and invoking Tools until the Task is done, fails, or is cancelled.
_Avoid_: bot, assistant, worker

**Memory**:
A preference or fact the user has explicitly asked AOS to keep, given to every future Agent.
_Avoid_: history, context, knowledge base

**Cost Limit**:
An optional ceiling on estimated model spend, per Task or per day, beyond which Agents pause for the user.
_Avoid_: budget, quota, spend cap

**Tool**:
A single named action an Agent can take on the Machine, such as running a command, moving a file, or downloading a URL.
_Avoid_: function, skill, capability, action

**Autonomy**:
The user's chosen level of how much an Agent may do without asking: `auto`, `confirm-risky`, or `confirm-all`.
_Avoid_: permission mode, safety level

**Approval**:
A pause in which an Agent waits for the user to allow or deny a specific Tool invocation before it happens.
_Avoid_: confirmation, consent, prompt

**Risky Action**:
A Tool invocation that needs Approval under `confirm-risky` Autonomy, such as deleting, uninstalling, uploading, or anything done with root authority.
_Avoid_: dangerous command, unsafe action

**Privileged Tool**:
A Tool that acts on the Machine with root authority; every invocation of one is a Risky Action.
_Avoid_: sudo tool, admin tool

**Session**:
A long-lived shell on the Machine. Every Task has its own Session; the user may open others, and may watch or type into a Task's Session.
_Avoid_: terminal (when you mean the Session), shell, PTY

### Protecting the user's data

**Protected Path**:
A file or folder that no Agent may change or delete without an Approval, at every Autonomy level including `auto`.
_Avoid_: important file, locked file, safe path

**Trash**:
The holding area for files deleted on the Machine, from which they can be restored until the user empties it or they expire.
_Avoid_: recycle bin, bin

**Audit Log**:
The permanent, append-only record of every Tool invocation, its outcome, and who approved it.
_Avoid_: history, activity log, transcript

### Software

**Install Ledger**:
The durable record of every software installation performed on the Machine; it is the authority on what software the Machine should have after a restart.
_Avoid_: history, install log, manifest

**Checkpoint**:
A named point in the Install Ledger, together with the configuration files changed after that point, to which the Machine's software can be Restored.
_Avoid_: snapshot, restore point, backup

**Restore**:
Returning the Machine's software and its configuration to the state recorded at a Checkpoint.
_Avoid_: rollback, revert, undo

**Replay**:
Re-applying the Install Ledger when the Machine starts, so that its software matches the Ledger.
_Avoid_: restore (that is returning to a Checkpoint), reinstall, sync

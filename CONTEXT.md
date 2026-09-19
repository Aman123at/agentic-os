# Agentic OS (AOS)

A personal Ubuntu environment where AI Agents carry out everyday computer work (moving files, installing software, downloading things) on the user's behalf, watched from a terminal or a macOS-style Desktop in the browser. It runs either directly on a Linux server or in a container.

## Language

### The environment

**Agentic OS (AOS)**:
The product name. Not an operating system in the kernel sense; it is a layer on top of Ubuntu.
_Avoid_: the OS (when you mean the Machine)

**Machine**:
The Ubuntu system that AOS runs on and that Agents act upon, including its filesystem, processes and installed software: a whole Linux server on a native install, a container on a Compose install.
_Avoid_: OS, container, VM, box, server, VPS

**native install** / **Compose install**:
The two ways AOS runs: installed on a Linux server, where the Machine is that server and Agents reach its real filesystem; or run under Docker Compose, where the Machine is the container. Lowercase, because they describe one Machine rather than name a second thing.
_Avoid_: deployment, edition, bare metal, self-hosted, Install (an Install Ledger is about software, not about how AOS runs)

**Daemon**:
The AOS program itself (`aosd`). systemd starts it, `aos daemon` controls it, and it runs every Agent, Tool and Service. It is not a Service.
_Avoid_: the server, the backend, the service (a Service is the user's own program)

**Service**:
A named, long-running program that the user asked AOS to keep running and to bring back after a restart. AOS supervises it itself: a Service is not a systemd unit, and systemd knows only about the Daemon.
_Avoid_: daemon (that is AOS itself), background job

**Account**:
The single username and password that signs the user in to the Desktop. There is exactly one, it is set in the Configuration, and on a server reachable from the internet it is the only thing standing in front of the Machine.
_Avoid_: login, profile, user (when you mean the Account)

**Configuration**:
The one file that holds every setting (`/etc/aos/config.yml`). The user may edit it directly, and changes made from the CLI or the Desktop are written back into it, so it never falls out of step with what is running.
_Avoid_: settings, preferences, config (in prose), environment variables

**Machine Profile**:
A concise, always-current description of the Machine (its Ubuntu version, installed software and Services) given to every Agent.
_Avoid_: system info, environment summary

**Mode**:
The way a user interacts with AOS, set by `mode` in the Configuration and changed with `aos mode`: `cli` (terminal only, with nothing listening on the network) or `ui` (terminal plus the Desktop). One binary carries both.
_Avoid_: flavour, variant, edition, image (Mode is not a property of a build)

**Realm**:
Which of the two ways the Machine is running: the **Standard** Realm or the **Root** Realm. The Machine runs one at a time, chosen at start from `root_mode` in the Configuration. Each Realm keeps its own history — Tasks, Audit Log, Memory, Notifications, Services, window layout and Browser profile — so nothing done in one is visible from the other. The Account (username and password) is shared.
_Avoid_: workspace, profile, context, session

**Root Mode** / **Standard Mode**:
Running in the Root Realm or the Standard Realm. In **Standard Mode** (the ordinary one) Agents, the Terminal and Finder act as the unprivileged `aos` user, confined by Landlock, and Protected Paths are enforced. In **Root Mode** they act as root: `sudo` is available, every file is unlocked and Protected Paths are not enforced. Switching between them shows a warning, asks for the password and restarts AOS into the new Realm. Root Mode's isolation from Standard history is a privacy boundary, not a security boundary against a root Agent.
_Avoid_: admin mode, superuser mode, god mode, sudo mode, incognito

**Desktop**:
The macOS-style graphical interface for the Machine, built as a web app and viewed in the browser. It runs only its own built-in apps, not Linux GUI programs.
_Avoid_: GUI, VNC, remote desktop, UI (when you mean the Desktop specifically)

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

**Model Catalogue**:
The list of models AOS offers, recording which reasoning efforts each one accepts and what it costs. It ships as data and the user can extend it; a model that is not in it can still be chosen, without cost tracking.
_Avoid_: model list, allow-list, supported models

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
The durable record of every software installation performed on the Machine, and the basis for Checkpoints. On a Compose install it is also the authority on what software the Machine should have after a restart.
_Avoid_: history, install log, manifest

**Checkpoint**:
A named point in the Install Ledger, together with the configuration files changed after that point, to which the Machine's software can be Restored.
_Avoid_: snapshot, restore point, backup

**Restore**:
Returning the Machine's software and its configuration to the state recorded at a Checkpoint.
_Avoid_: rollback, revert, undo

**Replay**:
Re-applying the Install Ledger when the Machine starts, so that its software matches the Ledger. It happens only on a Compose install, whose Machine starts empty each time; a native install keeps its software, so returning to a Checkpoint there is always the user's own choice.
_Avoid_: restore (that is returning to a Checkpoint), reinstall, sync

## Retired terms

Words the product no longer has. They still appear in old commits, in ADRs 0003–0008 and in [docs/m0-findings.md](docs/m0-findings.md), and they mean nothing in new writing.

Retirement binds **product language**: this glossary, the documentation, the Desktop's own words, the Agent prompt and the Machine Profile. It does not bind identifiers in code that mean something else — an HTTP `Host` header is HTTP's word, not ours.

**Shared Folder** — the folder that was visible both on the user's own computer and inside the Machine, used to move files in and out. Removed entirely: files move through the Desktop, and a Compose install can still bind-mount a folder into the home folder without AOS naming it.

**Host** — the computer on which the Machine ran. Its two jobs split. The computer AOS runs on is the **Machine**. The computer the user browses from is **your computer**, lowercase and undefined, because nothing about it is ours to decide: AOS only learns which keyboard shortcuts it prefers and where a download should land.

---
title: Restarts and work in flight
description: What happens to queued and running Tasks, Approvals and Services when the daemon stops or restarts.
---

The daemon can restart for many reasons: you clicked **Restart**, ran `sudo aos daemon restart`, upgraded,
switched Mode or [Root Mode](/use/root-mode/), rebooted the server, or it crashed and systemd brought it back.
**The same rules apply every time**, including unattended restarts, which is why they get a page of their own.

## What happens when it stops

The daemon shuts down in this order:

1. **The Desktop's front door on port 7700 closes first**, so no new work arrives.
2. **Running Tasks get up to about 45 seconds to settle.** Anything still running is stopped.
3. **The local control socket closes last**, so `aos` keeps working until the Tasks have settled.

## What you find when it's back

| Before the restart | After the restart |
| --- | --- |
| **Queued** Task | Still queued. It runs when a slot is free, oldest first. |
| **Running** Task | **Interrupted**, with the note *"AOS stopped while the Task was running; aos resume continues it."* |
| Task **awaiting you** (a question or an Approval) | **Interrupted** as well. |
| **Pending Approval** | **Denied**, recorded as decided by *aos: restarted*. |
| Tool call in progress | Marked cancelled: *"AOS stopped while this call was running."* |
| **Services** | Started again according to their restart policy. |
| Finished Tasks, Memory, Audit Log | Unchanged. |

## Continue an interrupted Task

Nothing is lost. The Agent picks up with its full history and is told that a restart interrupted it.

```bash
sudo aos tasks                 # find the Interrupted Task's id
sudo aos resume <id>
```

Or open the Task in the **Agent** app and resume it there.

## Before a planned restart

- **Check for running work** with `sudo aos tasks`, and let it finish if you can.
- **Upgrades wait for you.** Re-running `install.sh` refuses while any Task is running, queued or awaiting you.
  Set `FORCE=1` to upgrade anyway: those Tasks become Interrupted and you resume them afterwards.
- **Switching Root Mode is refused while a Task is running**, and names the Task. Cancel it or wait, then try
  again.

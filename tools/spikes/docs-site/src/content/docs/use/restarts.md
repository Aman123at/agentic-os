---
title: Restarts and work in flight
description: What happens to running Tasks when the daemon stops.
---

STUB — page map only. This page exists so the sidebar and the shape of the site are
judgeable; M6 writes it.

Must carry: queued Tasks survive untouched; running and awaiting-user Tasks are interrupted with an
`aos resume <id>` line; pending Approvals are denied. `aos daemon restart` and `aos mode` refuse
while a Task is running or awaiting you; `--wait` drains; `--force` interrupts, and does not
cancel. This happens on an unattended restart too, which is why it is a page and not a footnote.

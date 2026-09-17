---
title: What the password protects
description: The Desktop password is root on this server. Read this before you install on a machine that matters.
---

:::danger[Read this one]
On a native install, **the Desktop password is root on your server.** Anyone who signs in can
reach every file on the machine and, through an Approval they grant themselves, run any command as
root. There is no second factor and no second account.
:::

This page is not boilerplate. It is the consequence of three decisions that are individually
reasonable and, together, mean exactly what the box above says.

## Why it is true

**Agents run as the `aos` user, and that user can become root.** Privileged work — installing a
package, editing something under `/etc`, restarting a service — goes through a Privileged Tool,
which stops and asks you for an Approval and records what happened in the
[Audit Log](/agentic-os/reference/audit/). The asking is real: nothing privileged happens without
a human saying yes. But the authority is real too. The account that answers those questions is
the account that holds root.

**Agents can write outside their own home.** On a native install the machine *is* your server, so
Agents read the whole filesystem and can write most of it. A short list is protected — `/boot`,
`/proc`, `/sys`, `/root`, other users' home directories, and Agentic OS's own binaries and
configuration — and writing to anything else you mark as protected asks first. Everything else is
theirs to change.

**The sign-in page is on the public internet.** The daemon binds `0.0.0.0` so you can open the
Desktop from your laptop. Scanners will find port 7700 within hours of the install. That is
survivable — it is one password form, rate-limited, with a lockout — but it means the password is
load-bearing from the first minute.

## What to actually do

1. **Choose a real password.** Twelve characters is the enforced minimum and it is a floor, not a
   target. Use a password manager and a generated passphrase. The initial password the installer
   printed is refused as your new one.
2. **Change it on first sign-in.** You will be made to; do not work around it. The initial
   password was printed to a terminal and written to a file, and it is blanked from that file the
   moment you replace it.
3. **[Put TLS in front](/agentic-os/configure/nginx/)** if the server holds anything real. Plain
   HTTP means your password crosses the network in the clear, and any network between you and the
   server can read it.
4. **Know the recovery path before you need it.** There is no email reset, because there is no
   email. [`sudo aos user passwd` over SSH](/agentic-os/operate/lost-password/) is the only way
   back in. If you lose both the password and your SSH access, you have lost the machine.

## If this is more authority than you want to hand over

Run it [with Docker Compose](/agentic-os/start/compose/) instead. Agents get a full Ubuntu machine
and the same Desktop, but it is the container's filesystem, not your server's, and the blast
radius of a bad Task stops at the container. You give up the thing the native install exists for —
Agents that can actually administer your server — and in exchange you get a mistake you can delete.

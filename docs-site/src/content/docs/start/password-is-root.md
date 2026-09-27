---
title: What the password protects
description: On a native install, the Desktop password is effectively root on your server. Read this before you install on a machine that matters.
---

:::danger[Read this one]
On a native install, **the Desktop password is effectively root on your server.** Whoever signs in can open
the Terminal app and run `sudo` without another password, and can reach every file on the machine. There's
no second factor and no second account.
:::

This isn't boilerplate. It follows from three reasonable design decisions which, taken together, mean exactly
what the box above says.

## Why it's true

### 1. The `aos` user can become root

Agents run as the unprivileged `aos` user, and **Agents themselves can't use `sudo`**: the kernel's
`no_new_privs` flag blocks it inside their sandbox. Privileged work, such as installing a package, editing
something under `/etc` or restarting a service, goes through a Privileged Tool. That Tool **stops and asks you
for an Approval** and records what happened in the [Audit Log](/reference/audit/).

But *you*, the person signed in to the Desktop, are trusted fully. The Desktop's **Terminal** runs as `aos`,
and the installer gives `aos` passwordless `sudo`. The account that answers the Approvals is the account that
holds root.

### 2. Agents can write outside their home

On a native install the machine **is** your server, so Agents can read the whole filesystem and write most of
it. A short list is **protected**: `/boot`, `/proc`, `/sys`, `/root`, other users' home folders, and Agentic
OS's own binaries, configuration and state. Writing to those, or to anything else you
[protect](/reference/protect/), needs your Approval first.

### 3. The sign-in page is on the internet

The daemon listens on port **7700 on every network interface**, so you can open the Desktop from your laptop.
Internet scanners find open ports within hours. **Nothing else stands in front of that sign-in form except
your password**, so it matters from the first minute.

## What to do about it

1. **Choose a strong password.** Twelve characters is the enforced minimum, not a target. Use a password
   manager and a long generated passphrase.

2. **Change the one-time password on first sign-in.** You'll be made to. The installer printed it to a
   terminal, and it stops working the moment you set your own.

3. **Put HTTPS in front** if the server holds anything real. Plain HTTP sends your password across the
   network unencrypted. [Set up nginx and a free certificate](/configure/nginx/).

4. **Limit who can reach port 7700.** In your cloud firewall, allow only your own IP (see
   [Cloud providers](/start/cloud/)). Or keep 7700 closed entirely and reach the Desktop through nginx, a VPN
   such as Tailscale or WireGuard, or an SSH tunnel:

   ```bash
   ssh -L 7700:localhost:7700 you@your-server
   # then open http://localhost:7700 on your laptop
   ```

5. **Know the recovery path before you need it.** There's no email reset. [Recovery needs SSH
   access](/operate/lost-password/). If you lose both the password and SSH, you've lost the machine.

## If this is more trust than you want to give

Run it [with Docker Compose](/start/compose/) instead. Agents get a full Ubuntu machine and the same Desktop,
but it's the container's filesystem, not your server's, so the damage a bad Task can do stops at the
container. You give up what the native install exists for, Agents that can really administer your server,
and in exchange any mistake can simply be deleted.

---
title: Install on a server
description: One command installs the daemon, creates its user and starts it under systemd.
---

## Before you start

- **Ubuntu 22.04 or newer**, or Debian 12 or newer. Other distributions may work; these are the
  ones that are tested.
- **systemd.** The installer checks for it and stops cleanly if it is missing, because there is no
  second lifecycle to fall back on. [Docker Compose](/start/compose/) is the supported
  alternative.
- **root**, through `sudo`.
- **About 500 MB of disk**, plus whatever your Agents install later.
- An **OpenAI API key**. You do not need it to install — the daemon starts without one and
  [you add it afterwards](/configure/config-file/).

You do **not** need Go, Node, Docker or any other toolchain. The installer downloads a prebuilt
binary for your architecture and nothing else.

## Install

```sh
curl -fsSL https://amantiwari.co.in/agentic-os/install.sh | sudo sh
```

If you would rather read a script before running it as root — and you should — download it,
read it, then run it:

```sh
curl -fsSLO https://amantiwari.co.in/agentic-os/install.sh
less install.sh
sudo sh install.sh
```

Both forms do exactly the same thing. The first is the one people paste; the second is the one
people who have been burned paste.

## What it does to your server

| It creates | Why |
| --- | --- |
| `/usr/local/bin/aosd` and `/usr/local/bin/aos` | the daemon and the command line |
| a system user **`aos`** | Agents run as this user, never as root |
| `/home/aos` | the Agents' home, and the only tree they own outright |
| `/etc/aos/config.yml` | your configuration, root-owned and `0600` |
| `/var/lib/aos` | the database, the audit log and the signing key |
| `/etc/systemd/system/aos.service` | so it starts at boot and restarts if it dies |

It does **not** install a compiler, a Node runtime, a database or any of the other software the
Docker image ships with. A server is not a disposable container, and the installer does not get to
decide what is on yours. Agents install what a Task needs, through the
[Install Ledger](/reference/software/), and you can undo it.

The installer is idempotent. Every step checks before it acts, so **re-running it is both the
upgrade and the repair** — there is no rollback because there is nothing to roll back.

## First start

When it finishes, it prints something like:

```
Agentic OS is running.

  Desktop:  http://203.0.113.10:7700
  Username: aos
  Password: kq7m-4td2-9xbe-w1rr

Sign in and you will be asked to choose a new password.
```

Write the password down before you close the terminal. You can print it again with `aos status`
until the first sign-in replaces it.

Three things about that URL are worth knowing now:

- **It is reachable from the internet.** The daemon binds `0.0.0.0`, so a fresh install answers on
  port 7700 from anywhere that can route to your server. That is deliberate: a browser desktop you
  cannot open from your laptop is not a product. The password, not the bind address, is what
  stands between the internet and your server — so read
  [what the password protects](/start/password-is-root/).
- **The port may not be 7700.** If something already holds it, the daemon takes the next free port
  and writes its choice into `config.yml`, so it moves once and never again.
- **It is plain HTTP.** Your password crosses the network in the clear. If that is not acceptable
  — and on a server with anything real on it, it is not —
  [put nginx and a certificate in front](/configure/nginx/) before you sign in the
  first time.

## Next

1. [Sign in and set your own password](/start/sign-in/)
2. [Add your API key and choose a model](/configure/config-file/)
3. [Give it something to do](/reference/run/)

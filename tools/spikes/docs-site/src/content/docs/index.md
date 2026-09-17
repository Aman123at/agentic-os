---
title: What Agentic OS is
description: An Ubuntu machine your Agents run on your own server, watched from a browser desktop or a terminal.
---

Agentic OS turns a Linux server into a machine that OpenAI-powered Agents operate for you. You
give it a Task in English — "set up a Postgres instance and a backup cron", "read these CSVs and
build me a report" — and an Agent does the work with real shell commands, on a real machine, while
you watch. Anything with lasting consequences stops and asks you first.

There are two ways to run it, and they differ in one way that matters more than any other.

## Native install (recommended)

`install.sh` installs the daemon on the server itself. The machine the Agents work on **is your
server**: they read and write the real filesystem, install real packages with `apt`, and run real
services under systemd. That is the point of it, and it is also the risk of it — see
[what the password protects](/agentic-os/start/password-is-root/) before you install on a server
that holds anything you care about.

[Install it →](/agentic-os/start/install/)

## Docker Compose

The same product, confined to a container. Agents get a complete Ubuntu machine, but it is not
your server: the filesystem they see is the container's, and it goes away when the container does.
Use it to try Agentic OS, or to keep Agents away from a host you are not willing to hand over.

[Run it with Compose →](/agentic-os/start/compose/)

---

Both shapes give you the same two front ends: a macOS-style **Desktop** in the browser, and the
`aos` command line over SSH. Every command is in the [reference](/agentic-os/reference/).

---
title: Services and ports
description: Programs Agents keep running, and how to open one in your browser from the Desktop.
---

A **Service** is a program an Agent keeps running for you: a web app, an API, a database, a bot. Agentic OS
supervises Services itself. It starts them, restarts them according to their policy (`always`, `on-failure`
or `never`), keeps their logs, and **brings them back after the machine or the daemon restarts**.

You usually don't create Services by hand. Ask for one:

> Create a small Flask app that says hello, and keep it running as a Service on port 8000.

## Open a Service in your browser

:::note[Open it from the Desktop, not by typing a URL]
Forwarded Services are protected by your sign-in. The Desktop opens them with a **single-use ticket**, so a
hand-typed address like `http://server:7700/port/8000/` is refused.
:::

There are two places to open one:

1. **Activity Monitor → Services & Ports.** Each Service lists the ports it listens on, with an **Open :8000**
   button. Under *Other listening ports*, anything else listening on the machine has an **Open** button too.
2. **Notification Center.** When an Agent starts something that listens on a port, the notification offers to
   open it.

The page opens in a new tab at `http://<server>:7700/port/8000/`. After that first ticketed load, a cookie
scoped to that one port keeps it working for **12 hours**, so the page's images, scripts and API calls all
load normally.

For safety, forwarded pages run in a **sandbox** with their own origin. A Service's page can't act as the
Desktop or read your Desktop session.

### Docker Compose extras

- **`http://<port>.localhost:7700`** also works, because on Compose the Desktop really is on localhost. It's a
  convenience that only works in a browser on the same computer.
- To publish ports straight to your host, use `compose.override.yaml`. See
  [Publish Service ports](/start/compose/#common-recipes).

## Watch out for 0.0.0.0

A Service that listens on **`0.0.0.0`** (all interfaces) is reachable **directly** on its own port by anyone who
can reach your server, **without going through your Desktop password**. Activity Monitor marks such ports with
an *exposed* tag.

- Prefer Services that listen on `127.0.0.1` and open them through the Desktop.
- Or keep their ports closed in your cloud firewall.

## Manage Services from the terminal

```bash
sudo aos service list              # Services, plus every program listening on a port
sudo aos service logs web -f       # follow a Service's output
sudo aos service restart web
sudo aos service stop web          # until started again, or the next restart
sudo aos service start web
sudo aos service remove web        # stop and delete it (recorded in the Install Ledger)
```

See [`aos service`](/reference/service/) for every option.

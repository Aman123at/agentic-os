# Agentic OS

Turn a fresh Ubuntu or Debian server into a Machine that OpenAI-powered Agents operate for you. You give it a Task in plain English — "set up a Postgres instance and a nightly backup", "read these CSVs and build me a report" — and an Agent does the work with real shell commands on the real server, while you watch and steer from a macOS-style Desktop in your browser (or from a terminal). Anything with lasting consequences stops and asks you first.

Status: under development ([plan](docs/PLAN.md)). Documentation site: <https://agenticos.amantiwari.co.in/>.

## Install on a server

One command installs the daemon, creates its user, and starts it under systemd. You need Ubuntu 22.04+ (or Debian 12+), systemd, and root through `sudo`. You do **not** need Go, Node, Docker, or an API key to install — it fetches a prebuilt binary and nothing else.

```bash
curl -fsSL https://raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh | sudo sh
```

Or, since you should read a script before running it as root, download it first:

```bash
curl -fsSL https://raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh -o install.sh
less install.sh && sudo sh install.sh
```

When it finishes it prints the Desktop URL and a one-time password. Then:

1. Open `http://<your-server-ip>:7700` and sign in as `admin` with that password; it will make you set your own.
2. In System Settings, add your OpenAI API key (it is a secret, not config).
3. Give it your first Task.

That is the whole path from a bare VPS to a signed-in Desktop.

### The password is root, and the Desktop is on the internet

Read this before you sign in. The daemon binds `0.0.0.0:7700` — a fresh install answers on port 7700 from anywhere that can route to your server, deliberately, because a Desktop you cannot open from your laptop is not a product. It is plain HTTP, so your password crosses the network in the clear. And whoever signs in to the Desktop gets **passwordless root on the server**: Agents run as an unprivileged `aos` user, but the person driving them can `sudo` without a password.

So the password, not the bind address, is what stands between the internet and your server. Put it behind a firewall or a reverse proxy with TLS if that is not what you want, and choose a strong one.

## Try it locally with Docker Compose

Compose runs the same product inside a container. The filesystem the Agents see is the **container's**, not your host's — nothing they do touches the real machine — which makes it the safe way to try Agentic OS on your laptop before you install it on a server.

```bash
cp .env.example .env      # then put your key in OPENAI_API_KEY (it may stay empty)
docker compose up --build
```

- Open <http://localhost:7700> (`ui` Mode, the default). For a terminal instead, set `AOS_MODE=cli` in `.env` and run `docker compose exec aos aos`.
- The Browser app (`ui` Mode) is installed on demand — `docker compose exec aos aos browser install` — because the image no longer bakes Chromium in. A 🌐 Browser then appears in the Dock, and `aos browser remove` reverses it.
- `OPENAI_API_KEY` must exist in `.env`, even if empty. Without it, `docker compose up` stops with "required by secret … is not set".

**`docker compose up` hangs with the container stuck in "Created" (macOS).** Docker Desktop cannot mount folders under `~/Desktop`, `~/Documents`, or `~/Downloads` until macOS allows it, and a bind mount into home is such a mount when this repository lives in one of them. Enable the folder in System Settings → Privacy & Security → Files & Folders → Docker and restart Docker Desktop, move the repository elsewhere (e.g. `~/src/agentic-os`), or set `AOS_UID`/`AOS_GID` and mount from a path outside those folders.

## Learn more

- The [documentation site](https://agenticos.amantiwari.co.in/) — install, configure, and command reference.
- [docs/PLAN.md](docs/PLAN.md) — the design and the milestone plan.
- `docker compose exec aos aos doctor --host-check` (Compose) or `sudo aos doctor` (native) prints a pass/fail report of the sandbox, Sessions, secret handling, and forwarding.

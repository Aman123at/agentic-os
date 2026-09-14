# Agentic OS

An Ubuntu Machine in Docker where OpenAI-powered Agents carry out everyday computer work. Watch and steer them from a terminal (`cli` Mode) or from a macOS-style Desktop in the browser (`ui` Mode).

Status: under development ([plan](docs/PLAN.md)). The full README arrives with the release milestone (M5).

## Quick start

```bash
cp .env.example .env      # then put your key in OPENAI_API_KEY (it may stay empty)
docker compose up --build
```

- `cli` Mode: set `AOS_MODE=cli` in `.env`, then run `docker compose exec aos aos`.
- `ui` Mode: open http://localhost:7700.

`OPENAI_API_KEY` must exist in `.env`, even if empty. Without it, `docker compose up` stops with "required by secret … is not set".

## Troubleshooting

**`docker compose up` hangs with the container stuck in "Created" (macOS).** Docker Desktop cannot mount folders under `~/Desktop`, `~/Documents` or `~/Downloads` until macOS allows it. The Shared Folder (`./shared`) is such a mount when this repository is in one of those folders. Do one of the following:

- System Settings → Privacy & Security → Files & Folders → Docker → enable the folder, then restart Docker Desktop.
- Move the repository elsewhere, e.g. `~/src/agentic-os`.
- Set `AOS_SHARED_DIR` in `.env` to a folder outside them.

**Checking a Host.** `docker compose exec aos aos doctor --host-check` prints a pass/fail report of the sandbox, Sessions, secret handling and forwarding.

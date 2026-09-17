# /etc/aos/config.yml: schema, validation and the `aos config` verbs

Type: grilling
Status: open
Blocked by: —

## Question

Settled: the file lives at `/etc/aos/config.yml`, root-owned `0600`, outside the Agents' writable tree; runtime changes made via `aos config set` or System Settings are written back into it, so the file stays the single source of truth.

Specify it:

1. **The schema.** Which keys, their types and defaults. Candidates from today's `internal/config/config.go`: mode, port, bind, model, reasoning_effort, api_key, base_url, autonomy, max_tasks, max_retries, trash retention/size, cost limits, require_landlock, include_browser, timezone. Which of these are startup-only and which can change live? `internal/settings` already draws that line for a subset.
2. **Which settings the UI and `aos config set` may change**, and what happens to a startup-only key set at runtime — refuse, or accept and say "restart to apply"?
3. **Write-back mechanics.** Rewriting a YAML file the user hand-edits will destroy their comments and ordering unless handled deliberately. Round-trip preserving, a managed block, or a separate overlay file that is merged? This is the crux of the ticket.
4. **Bad config.** A malformed or unparseable file at startup: refuse to start with a precise error, or fall back to defaults and warn? A service that silently starts with `autonomy: auto` because a key was misspelled is dangerous.
5. **Validation and error messages.** `aos config set model=nope` must fail clearly; `aos config get`, `aos config path`, `aos config edit` behaviour.
6. **The API key.** `aos config set api_key=sk-…` leaves the key in shell history and in `ps`. Warn, refuse, or accept silently? Does the key sit in `config.yml` in plaintext or get moved to `/var/lib/aos/keys/openai` (which is already `Hidden` from Agents)?
7. **A YAML dependency.** The repo has none today; `go.mod` would gain one. Or the file could be a format the stdlib already parses.
8. **Compose parity.** Does the Compose path read the same file, or keep environment variables? Two configuration systems would double the documentation.


## Constraint from *Widening the filesystem to the whole VPS* (2026-09-17)

Question 6 of this ticket is **partly settled already**: the OpenAI key does **not** go in `config.yml`. `/etc/aos` is Hidden from Agents because `config.yml` holds the initial password, and the key belongs with the other secret state in `/var/lib/aos/keys/openai`, which is already Hidden by `internal/sandbox/policy.go`. What remains open here is the `aos config set api_key=…` ergonomics — shell history and `ps` — not the storage location.

Also settled there: `/etc/aos` being Hidden makes `/etc` a split directory for the Landlock ruleset, so every package install re-plans. Accepted, to be measured on the VPS.

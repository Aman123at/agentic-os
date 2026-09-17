# /etc/aos/config.yml: schema, validation and the `aos config` verbs

Type: grilling
Status: resolved
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


## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations").

### Two findings that reshaped the ticket

**There is already a persisted source of truth, and it is not a file.** `internal/settings/settings.go:1` documents the chain — a value saved from System Settings beats the environment, which beats the built-in default — and "saved" means a **SQLite table**. Nine settings live there today. Making `config.yml` the single source of truth with write-back would leave two persisted stores for the same nine keys.

**The restart warning Aman asked for by name becomes false.** He originally described the semantics as: set the config, restart, and the file takes over, resetting anything changed at runtime — and asked for that warning in the first-time setup documentation. Charting then took write-back (Q19 option (b)), where runtime changes are written *into* the file, so a restart never reverts anything. **Flagged explicitly and confirmed: the warning is dropped and the documentation says the opposite.**

### Decisions

1. **The settings table loses its role as storage.** `SettingsService` and `aos config set` both read and write `config.yml`; `0005_m6.sql` drops the settings table. One store, one precedence chain — `config.yml` > built-in default — and the environment layer disappears in the native install.
2. **The documentation says the reverse of the original warning**: what you change in the UI or the CLI is written to `config.yml`, and the file is always what is in force. A restart never silently reverts a setting.
3. **Write-back uses `yaml.v3`'s `Node` API**, which round-trips comments and key order. Decode into a `yaml.Node`, edit the mapping, re-encode, write a temp file in `/etc/aos` and `rename()` atomically with mode `0600` preserved. No managed block and no overlay file. Honest caveat for the docs: comments and key order survive, but exact blank-line placement and indentation normalise. Concurrent writes take a process-level lock.
4. **Live keys** are the nine that already are: `model`, `reasoning_effort`, `autonomy`, `max_tasks`, `max_retries`, `task_cost_limit_usd`, `daily_cost_limit_usd`, `trash_retention_days`, `trash_max_gb`. **Startup-only**: `mode`, `port`, `bind`, `include_browser`, `require_landlock`, `base_url`, `uid`/`gid`, `username`.
5. **`aos config set` on a startup-only key accepts, writes, and says "restart to apply"** — refusing is obstructive when the file is the source of truth. **`aos config get` marks those keys *(pending restart)*** so nothing lies about what is actually in force.
6. **A malformed file refuses the start**, naming the key, the line and the reason; never a fall back to defaults. **An unknown key is an error too**, not ignored — a typo'd `task_cost_limit_usd` that silently means "no limit" is exactly the failure this rule exists to prevent. Accepted cost: a config written by a newer version will not load on an older one, which is the right trade for a single-user appliance.
7. **Verbs**: `aos config get [key]`, `set k=v`, `path`, `edit` (opens `$EDITOR`, validates before saving, refuses to save invalid), `validate`.
8. **`set model=…` and `set reasoning_effort=…` validate against the model catalogue**, not the fixed list at `internal/settings/settings.go:74` — which is the live defect *The model catalogue* found: missing `max`, still offering the legacy `minimal`, and per-model in reality where a wrong value is an HTTP 400 rather than a clamp.
9. **The API key** lives in `/var/lib/aos/keys/openai` (settled by *Widening the filesystem*). `aos config set api_key=sk-…` is accepted **with a warning that it is now in shell history and was visible in `ps`**; `aos config set api_key` with no value reads it from `/dev/tty`, and that is the form the documentation shows. `aos config get api_key` prints it masked.
10. **Compose parity: `aosd` always has a config file.** Under Compose it is the same `/etc/aos/config.yml` inside the container, **generated from the environment on first start when absent**. One mechanism, one code path, one documentation section; a Compose user who wants persistence bind-mounts the file. Keeping env-only for Compose would leave runtime settings with nowhere to write back and force the SQLite table to survive for that case alone.

### Consequences

- `internal/config`'s `FromEnv` becomes `FromFile` plus an env-seeding path used once, on a Compose first start. `Config.Set` (which names the variables the environment set) and `settings.Source`/`Setting.Fallback` all simplify: there is no longer an environment layer to attribute a value to.
- `SettingsService` keeps its shape but its backing store changes from SQLite to the file.

## Input from *Installing Chromium lazily, and Compose parity* (resolved 2026-09-17)

`include_browser` stays startup-only, but it is no longer only hand-written: **`aos browser install` writes it `true` on success and `aos browser remove` writes it back to `false`**, through the same write-back path as any other runtime change. Its documented meaning changes with it — "the browser is installed and on", not "fetch the browser at the next start". Nothing about the key's validation changes; `INCLUDE_BROWSER` disappears as a build argument, surviving only as a Compose seed like the other keys.

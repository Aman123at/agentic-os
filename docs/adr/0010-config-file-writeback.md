---
status: accepted
---

# Configuration lives in one file, and runtime writes back to it

`/etc/aos/config.yml` is the single source of truth for every setting that is not
a secret. It replaces the SQLite `settings` table, which was the real source of
truth until now (`internal/settings`), so there is one store and one precedence
chain. The file is root-owned `0600`, deliberately outside `/home/aos` (the
Agents' writable tree). Runtime changes — from System Settings or `aos config set`
— are **written back** into the file, so a restart re-reads it and never silently
reverts a setting the user made at runtime.

## Consequences

- **Write-back uses `yaml.v3`'s `Node` API**, which round-trips comments and key
  order, written atomically via a temp file plus `rename()`. A file the user
  hand-edits keeps its comments after AOS writes to it.
- **A malformed file, or an unknown key, refuses the start** rather than falling
  back to defaults — a silent fallback would hide a typo that disables a setting
  the user believes is on. Startup-only keys are accepted, written, and reported as
  *(pending restart)* rather than applied live.
- **The "restart resets your runtime settings" warning is now false**, and the
  documentation says the opposite: because runtime changes are written back, the
  file and the running state never diverge. This reverses the warning Aman
  originally asked to document.
- **The port is written back on first start** if the base port was taken: `aosd`
  scans upward, writes the chosen port into the file (so it moves once, never
  again) and prints it from `aos status`. An explicit `port:` pins it and fails
  loudly instead of moving.
- **`gopkg.in/yaml.v3` is a new dependency.** Accepted: there is no stdlib YAML,
  and a hand-edited file needs comments. Password hashing stays stdlib
  (`crypto/pbkdf2`) to keep the count low (ADR-0002).
- **Secrets stay out of the file.** The OpenAI key and the JWT signing key live in
  `/var/lib/aos/`, root-only, never in `config.yml` (ADR-0007).
- **Compose generates the same file** from its environment on first start, so the
  two install shapes share one configuration system rather than forking into
  env-vars-here, file-there.

## Considered Options

- **Keep the SQLite `settings` table as the source of truth:** rejected; a server
  operator expects to edit a config file and `grep` it, and two stores mean two
  precedence bugs.
- **Read-only file, runtime settings in the database:** rejected; a restart would
  then revert runtime changes, which is exactly the surprise write-back removes.
- **Env-vars only (as Compose does today):** rejected for the native install; env
  vars are awkward to change at runtime and cannot carry comments.
- **TOML or JSON:** rejected; YAML with comments is friendliest for a file a person
  edits, and `yaml.v3` preserves them.

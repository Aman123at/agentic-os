---
status: accepted
---

# Root Mode is a separate Realm, chosen at start and switched by restart

The Machine can run in one of two **Realms**. The **Standard** Realm is the
ordinary one built up to M6: every Agent, the Terminal and Finder act as the
unprivileged `aos` user, confined by Landlock and `no_new_privs`, and Protected
Paths are enforced. The **Root** Realm — **Root Mode** — runs the whole Machine
as root: Agents can run anything that needs `sudo`, every file and folder is
unlocked, and Protected Paths are not enforced.

The Realm is chosen once, at start, from `root_mode:` in `/etc/aos/config.yml`
(a startup-only key, ADR-0010). The Daemon resolves it into an
`internal/realm.Realm` value and hands that to everything that needs it; no other
package reads the key. Every switch — from the System Settings switch or
`aos root on|off` — writes the key and **restarts `aosd`, then reloads the
Desktop**, so the new Realm is in force from a clean start rather than flipped
inside a running process.

## Consequences

- **Each Realm keeps its own history in its own database file** (M7.2): Tasks,
  Audit Log, Memory, Notifications, Services, window layout and Browser profile.
  The API only ever serves the Realm in force, so neither the Desktop nor the CLI
  can show the other Realm's history. This is a **privacy boundary between the two
  histories, not a security boundary against a root Agent** — a root Agent can, if
  it tries, get around anything on the Machine, including AOS itself. The warning
  shown before switching in says so plainly.
- **Switching restarts the Daemon.** The switch reuses one Restart operation
  (`SystemService.Restart`, M7.3) rather than growing its own: write the key, then
  restart. `SystemService.Info` carries a random `boot_id`, new on every start, so
  a client confirms the restart happened by watching it change.
- **Switching into Root Mode is guarded.** It shows a warning of the consequences,
  needs an explicit "I understand", and asks for the Desktop account's password
  (M7.7). The generic settings path refuses `root_mode` (`ErrRootModeNotHere`) so
  the guard cannot be walked around with `aos config set`.
- **The account is shared, the histories are not.** `users` and `refresh_tokens`
  always come from the Standard database, so one sign-in survives a switch. The
  Cost Limit counts both Realms' spend so Root Mode cannot dodge it (M7.2).
- **A separate file, not a `realm` column.** A forgotten `WHERE realm = ?` in one
  query would leak history across the boundary; with one database file per Realm
  there is no query that can.

## Considered Options

- **A `realm` column on every table:** rejected; the isolation would then rest on
  every query remembering to filter, and one that forgets leaks the other Realm's
  history. One file per Realm makes the leak impossible.
- **Switch Realms live, without a restart:** rejected; the uid, Landlock policy,
  HOME, database handles and Service set all differ between Realms, and rebuilding
  them inside a running process is far more fragile than a clean restart into the
  chosen Realm.
- **A `sudo`-only escalation with no separate history (like `sudo -s`):** rejected;
  the user asked for the two histories to be isolated the way an incognito window
  is, which a shared history cannot give.
- **A second, always-on root Machine:** rejected; that doubles the running system
  and the attack surface. Root Mode is one Machine that boots one way or the other.

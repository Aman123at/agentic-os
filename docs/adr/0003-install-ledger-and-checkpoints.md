# Installed software is reconstructed from the Install Ledger, not persisted as filesystem state

Recreating the container discards everything outside volumes, so software an Agent installed would silently vanish. Instead of mounting volumes over system directories, every installation is recorded in the Install Ledger and re-applied when the Machine starts, from a package cache volume so it is fast and version-exact. Checkpoints are named positions in the Ledger plus copies of the configuration files changed after them, which makes them nearly free to create automatically before every Task that installs or removes software.

## Consequences

- Only installations that go through a Tool are recorded; this is why Agents have no direct `sudo` (see ADR-0004).
- Home directories stay persistent via a volume; user-space installs (pipx, npm prefix, mise) persist naturally and are still recorded.

## Considered Options

- **Volumes over `/usr`, `/etc`, `/var`**: rejected; breaks on image upgrades as the base image and the volume diverge.
- **Accept ephemeral system state**: rejected; violates the expectation that "install X" is lasting.
- **Full filesystem snapshots as Checkpoints**: rejected; hundreds of MB and seconds each, too heavy to take automatically.

## M6 amendment (native install, 2026-09-17)

The opening premise — "recreating the container discards everything outside
volumes" — is the **Compose install's**. On a **native install** (ADR-0009) the
Machine is a persistent Ubuntu server: nothing is discarded on a restart, so
**Replay is off at boot**. Re-applying the whole Ledger to a live server is
needless and destructive (it would `apt-get`-pin packages and rewrite `/etc` the
user may have changed). The Install Ledger is still recorded, and Checkpoint and
Restore stay available **user-initiated**, so "restore to before that Task" still
works on demand. Whether `/etc` Checkpoints still earn their place once Replay is
off is left open (§21). See M6.9.

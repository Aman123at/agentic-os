---
title: Configuration
description: /etc/aos/config.yml, key by key — and why a restart never reverts what you changed.
---

STUB — page map only. This page exists so the sidebar and the shape of the site are
judgeable; M6 writes it.

Must carry: every key, in the order the installed template lists them, saying the same things that
template's comments say. **The restart note goes here, and it is the opposite of what people
expect:** changes you make from the Desktop or with `aos config set` are written back into
`config.yml`, so the file stays the single source of truth and a restart does not revert them.
Keys that only take effect at startup are accepted, written, and marked *(pending restart)*. An
unknown key or a malformed file refuses the start rather than falling back to defaults.

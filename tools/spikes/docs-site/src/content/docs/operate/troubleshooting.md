---
title: Troubleshooting
description: It will not start, it is not reachable, an Agent cannot do something.
---

STUB — page map only. This page exists so the sidebar and the shape of the site are
judgeable; M6 writes it.

Must carry: `aos status` and `aos doctor` first; `journalctl -u aos`; a config error refuses the
start, so read the journal rather than guessing; the port moved and where it is written down;
nothing answers on 7700 (firewall, cloud security group); an Agent says it cannot write somewhere
— protected paths and how to unprotect one.

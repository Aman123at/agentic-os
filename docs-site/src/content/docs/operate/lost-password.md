---
title: If you lose the password
description: sudo aos user passwd is the only way back in.
---

STUB — page map only. This page exists so the sidebar and the shape of the site are
judgeable; M6 writes it.

Must carry, plainly and near the top: **there is no email reset and no reset link.** SSH to the
server and run `sudo aos user passwd`. It works whether or not you are locked out, and it clears
the lockout counters. If you have lost SSH access as well, the machine is not recoverable.

---
status: accepted
---

# The aosd API always requires authentication, even on localhost

The API is always authenticated. On a native install (ADR-0009) it binds
`0.0.0.0` and faces the internet, so authentication — not a loopback address — is
what stands between a stranger and the Machine. A single user signs in with a
username and password (hashed with stdlib `crypto/pbkdf2`, stored in SQLite). The
Desktop holds a short-lived JWT **access token** in memory and a **refresh token**
in `localStorage` (15 min / 30 days, rotated on use, with family revocation on
replay), and every request carries the token in a **header, never a cookie**, so
DNS rebinding gains nothing and the old `Host` allow-list is deleted. Loads that
cannot send a header — WebSockets, `<img>`/`<video>` on `/files/raw`, PDF ranges,
the download anchor — are each authorised by a **single-use 30-second ticket**.
Every request's `Origin` is checked on RPC and WebSocket upgrades: this is the real
CSRF defence and it handles an arbitrary public host. The in-Machine CLI uses a
Unix socket that rejects Agent-confined callers, and path-forwarded Service pages
are served in a CSP sandbox behind a cookie scoped to `Path=/port/<n>/` that is
exchanged from the ticket **inside the authenticator**, so they cannot ride on the
Desktop's sign-in; a Service that binds `0.0.0.0` bypasses the forwarder and is
flagged as publicly reachable instead. In `cli` Mode there is no TCP listener at
all. This strengthens — it does not reverse — the earlier decision to
authenticate even on localhost.

## Considered Options

- **No auth on localhost, Origin/Host checks only**: rejected; blocks websites but not Host programs or scripts inside the Machine.

## M6 amendment (2026-09-17)

The opening paragraph above is the M6 model and supersedes the earlier mechanism
(a first-start access token, a one-time sign-in link, an HttpOnly/SameSite cookie,
and a request-time `Host` allow-list). `AOS_ACCESS_TOKEN` and `aos desktop-url` are
deleted. Because `http://<ip>:7700` is not a secure context, Service Workers,
`crypto.subtle` and the async clipboard API are unavailable; auth no longer depends
on any of them. See M6.3/M6.4 in the plan, and the tickets *Reach, bind and the two
Host checks*, *The authentication model* and *The authentication screens and account
lifecycle*.

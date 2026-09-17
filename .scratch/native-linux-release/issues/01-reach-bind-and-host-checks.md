# Reach, bind and the two Host checks

Type: grilling
Status: open
Blocked by: —

## Question

`bind: 127.0.0.1` and "the user can also just open `http://IP:PORT`" cannot both be true. On loopback, only something else on the VPS can reach `aosd`, so **nginx stops being optional and becomes mandatory** — a browser on a laptop gets connection-refused before any authentication runs.

Separately, the `Host` header is checked in two independent places — `internal/api/auth.go:131` (`localHost`, commented as the DNS-rebinding defence) and `internal/proxy/proxy.go:44` — and both answer `421 Misdirected Request` for anything that isn't `localhost`, `127.0.0.1` or `::1`. An nginx `proxy_pass` forwards the user's real `Host` by default, so **a correct nginx setup is refused today** unless nginx is told `proxy_set_header Host localhost;`.

Settle:

1. ~~Is loopback-only, nginx-required the intended experience, or should `bind` default to `0.0.0.0`?~~ **Settled 2026-09-17: `0.0.0.0`.** A fresh install is reachable at `http://IP:7700`; nginx and TLS stay optional.

   This makes question 2 mandatory rather than a preference. A browser opening `http://1.2.3.4:7700` sends `Host: 1.2.3.4:7700`, which **both** checks refuse with 421 — so on the settled bind address, `aosd` as it stands today answers nothing but 421 to every real user. Relaxing the checks is now on the critical path, not a nicety.

   It also moves the whole security posture onto the password screen. Port 7700 open to the internet will be found by scanners within hours, so *The authentication model* now carries weight it did not carry when the answer was loopback: rate limiting on the sign-in, a sane lockout, and what happens before an account exists all become part of this effort rather than later hardening.
2. Do the two `Host` checks get relaxed (accept any hostname, or an allow-list from `config.yml`), or do they stay strict with a documented `proxy_set_header Host localhost;`? Note that once the JWT lives in an `Authorization` header rather than a cookie, DNS rebinding stops being the threat the check was written for — which weakens the argument for keeping it.
3. What does `bind:` accept in `config.yml`, and what does `aos service status` print about reachability so a user isn't left guessing why their browser can't connect?
4. What do we do about the Service port-forwarding feature (`<port>.localhost:7700`, `/port/<port>/`), which is built on the same Host parsing and assumes localhost?

The answer feeds the nginx snippet in the docs and an amendment to ADR-0007.

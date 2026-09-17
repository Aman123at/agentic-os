# Reach, bind and the two Host checks

Type: grilling
Status: resolved
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


## Answer

Settled 2026-09-17 with Aman over one grilling round.

### Three facts from the code that shaped it

- **`bind` is not a listener setting today, it is a Docker setting.** `aosd` already listens on `:7700` — all interfaces — at `internal/daemon/daemon_linux.go:198`. What enforces loopback is `"${AOS_BIND}:${AOS_PORT}:7700"` in `compose.yaml:19`, Docker's publish address. `AOS_BIND` reaches Go only to print a warning (`cmd/aosd/main.go:48`). So `bind: 0.0.0.0` is close to a no-op in the listener; **all the work is in the Host checks.**
- **The Origin check is already host-agnostic.** `internal/api/auth.go:135` compares `Origin` against `"http://"+r.Host` — whatever Host arrived. Relaxing `localHost` therefore costs **no** CSRF protection. The only thing `localHost` adds is DNS-rebinding defence, and rebinding is an attack on *loopback* services; once the port is public it buys an attacker nothing that connecting directly does not.
- **Subdomain port-forwarding cannot work remotely.** `<port>.localhost:7700` relies on the browser resolving `*.localhost` to loopback — on the user's laptop that is *the laptop's* loopback, not the VPS. `internal/cli/services.go:54` prints exactly that advice today and would be silently wrong.

### Decisions

1. **Delete both Host checks.** Remove `localHost` from `auth.go`'s `TCP` and the `name != "localhost" && …` guard from `internal/proxy/proxy.go:44`. The Origin check stays exactly as it is.
2. **No `allowed_hosts` key.** A knob nobody sets is documentation debt; Origin plus a password is the defence. It can be added later if a concrete need appears.
3. **Tokens live in headers, never cookies — and this ticket constrains *The authentication model* to that.** Deleting the rebinding defence is only safe if the browser never sends credentials automatically. An `Authorization` header is not sent automatically; an `HttpOnly` refresh cookie is. Cookie-based refresh would revive DNS rebinding as a live threat against anyone whose 7700 is firewalled to a LAN. The two decisions are each safe alone and unsafe together, so the constraint is recorded here rather than left to be rediscovered.
4. **Port forwarding**: `/port/<n>/` is the form that works remotely and becomes the documented one. `<port>.localhost` keeps working when the Host really is localhost, which is still right under Compose. `services.go:54` must print the URL that works for the *current* bind instead of a fixed localhost one.
5. **`bind:` accepts an IP literal only** — `0.0.0.0` (native default), `127.0.0.1`, `::`, or a specific interface address. A hostname is refused loudly at startup rather than resolved. **Compose keeps `127.0.0.1`**: a container on a laptop is a sandbox, a VPS is a server, and they should not share a default.
6. **`aos service status` prints reachability**: the bind address, the port actually bound (it can move — see the port-scan decision), and the machine's primary outbound IP, so nobody has to guess the URL.

### The first-run window

With `0.0.0.0` by default, the gap between `aos service start` and the moment a password exists is the sharpest hazard in the effort: scanners find a new open port within hours, and whoever arrives first would own an LLM agent with sudo. A claim-token-gated signup screen was recommended; **Aman chose the appliance pattern instead, and it is a better fit for a config-file-driven product**:

1. **There is no signup screen at all.** The initial `username` and `password` are set by the user in `/etc/aos/config.yml` (root-owned `0600`) during the configure phase, before the service is useful.
2. **On first start `aosd` hashes that password into the `users` table** and never verifies against the plaintext, so there is exactly one verification path. This is the first table of its kind — `internal/store/migrations/` stops at `0004_m4.sql`, so M6 brings `0005_m6.sql` with `users` and the refresh-token table *The authentication model* needs.
3. **The first successful UI login forces a password change.** It is mandatory, not a suggestion, and the new password must differ from the initial one.
4. **After the reset, `aosd` blanks the `password` key in `config.yml`** using the write-back mechanic already settled, so a plaintext password does not linger on disk forever.
5. From then on it is the ordinary hashed-and-salted flow with normal JWT issuance.

**Three sub-decisions taken on Aman's behalf** (he delegated the rest of the round), each flagged for objection:

- **The pre-reset token must be restricted to the password-change call.** This is the classic hole in every forced-password-change flow: if the JWT issued at first login is a normal one, the screen is cosmetic and anyone holding the initial password just calls the API directly and skips it. The token carries a `must_change_password` claim and every other endpoint refuses it.
- **If `password` is unset in `config.yml`, `aosd` generates a random one on first start, writes it into the file, and `aos service status` prints it.** A default install is then never open *and* never unusable. Refusing to start would strand a user who skipped the configure phase; leaving the UI open would recreate the race this whole decision exists to close.
- **The account is only required in `ui` Mode.** In `cli` Mode nothing is exposed and no account is created; switching `cli` → `ui` is what triggers the initial-password requirement. Hands off to *Mode switching and the single binary*.

**Carried to *The authentication model*:** the sign-in endpoint now faces the open internet with a credential whose location is publicly documented, so rate limiting and a lockout are in scope for M6 rather than later hardening.

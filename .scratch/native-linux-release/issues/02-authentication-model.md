# The authentication model: JWT, refresh, and the WebSockets

Type: grilling
Status: resolved
Blocked by: —

## Question

Aman specified: username + password in the existing SQLite database, one user, a JWT access token, a working refresh token on both client and server, and no authentication on the CLI. This replaces the current model — an access token plus a one-time login code exchanged for an `HttpOnly; SameSite=Strict` session cookie (`internal/api/auth.go`), reached via `aos desktop-url`, which Aman wants removed.

Settle the mechanism:

1. **Where does the browser keep each token?** Access token in memory and refresh token in an `HttpOnly` cookie is the usual answer, but it reintroduces cookies (and CSRF) that a header-only design avoids. Both in `localStorage` is simplest and is exposed to any XSS in the Desktop.
2. **The WebSockets.** A browser `WebSocket` **cannot set an `Authorization` header**. The Desktop uses sockets for the session/event feed and for `GET /ws/browser`. Options: the `Sec-WebSocket-Protocol` subprotocol carrying the token, a short-lived single-use ticket fetched over RPC then passed as a query parameter, or keeping a cookie just for the socket upgrade. A token in a plain query string lands in logs.
3. **Lifetimes and rotation.** Access-token TTL, refresh TTL, whether refresh tokens rotate on use, and what happens when a rotated token is replayed (the standard signal of theft).
4. **Server-side state.** Refresh tokens need to be revocable for logout to mean anything, so they need a table and a migration (`internal/store/migrations/`). Are access tokens stateless JWTs verified by signature only?
5. **The signing key.** Where it lives, and what happens to live sessions if it is regenerated. Today sessions are HMACed against the access token, so rotating it signs everyone out — a property worth keeping or consciously dropping.
6. **Password hashing.** `golang.org/x/crypto` is **not** a dependency today. argon2id would add it; Go's stdlib `crypto/pbkdf2` (Go 1.24+) avoids a new dependency. Pick one.
7. **What replaces `AOS_ACCESS_TOKEN` and `aos desktop-url`** for scripts and for `tools/e2e`, both of which authenticate with the bearer token today.
8. **The Unix socket path is unchanged** — confirm the CLI keeps authenticating by peer credentials and gains nothing new.


## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations").

### Two findings that reshaped the ticket

**The header-only constraint from *Reach, bind and the two Host checks* breaks four things, not one.** The ticket anticipated WebSockets. But the session cookie is what authenticates *every* browser-initiated load, and the Desktop has four kinds:

- `/ws/session/<id>` (`desktop/src/apps/terminal/term.ts:112`) and `/ws/browser` (`desktop/src/apps/browser/page.ts:101`)
- `<img>` and `<video>` sources on `/files/raw` (`desktop/src/apps/finder/fs.ts:114`)
- the PDF viewer's Range requests
- the anchor download at `desktop/src/apps/finder/fs.ts:125` (`a.download = name`)

`mediaLoad` (`internal/api/auth.go:177`) exists precisely so the cookie can serve these. Removing cookies removes the mechanism all four depend on.

**`http://IP:7700` is not a secure context.** This removes Service Workers — so "a Service Worker injects the `Authorization` header" is *unavailable*, not merely heavy — along with `crypto.subtle` and the async clipboard API. Anything in M6 that assumes a secure context has to be checked against this.

### Decisions

1. **One ticket mechanism for all four browser-initiated loads.** An RPC returns a **single-use ticket, ~30 s, scoped to a path**; the Desktop appends `?t=…` to the WebSocket URL, the `<img>`/`<video>` source, the PDF ranges and the download anchor. Rejected: a path-scoped cookie (reopens the automatic-credential hole that ticket 01's decision depends on being closed), a Service Worker (impossible, see above), and `blob:` URLs (breaks Range, so video seeking and the PDF viewer regress, and it holds whole files in memory). Accepted cost: tickets land in access logs — single-use and 30 s makes a logged one worthless, and the only logs are ours.
2. **WebSockets use that same ticket, not `Sec-WebSocket-Protocol`.** The subprotocol trick works but only for WebSockets, so a second mechanism would still be needed for `<img>` and the download. One concept beats a tidier handshake.
3. **Refresh token in `localStorage`, access token in memory only.** The ADR must state the downside plainly: `localStorage` is readable by any XSS in the Desktop. With cookies ruled out there is no `HttpOnly` option, and a memory-only refresh token signs the user out on every page reload. Short access TTL plus rotation is what bounds the damage.
4. **Lifetimes**: access **15 minutes**, refresh **30 days** (matching today's `sessionMaxAge`), **rotated on every use**. A rotated refresh token presented a second time is treated as theft: revoke the entire family and sign that account out everywhere.
5. **Server-side state**: `users` and `refresh_tokens` land in `0005_m6.sql` — the first new migration since `0004_m4.sql`. Access tokens are stateless and verified by signature; **refresh tokens are stored as SHA-256 hashes**, so the database is not itself a credential store.
6. **The signing key** is 32 random bytes generated at first start into `/var/lib/aos/`, which is already `Hidden` from Agents by `internal/sandbox/policy.go`. **Not** in `config.yml`, which is hand-edited, copied and backed up. Rotating the key signs everyone out — that is today's property at `internal/api/auth.go:88` and it is kept deliberately.
7. **Password hashing**: Go's stdlib `crypto/pbkdf2`, settled earlier. No `golang.org/x/crypto`.
8. **`AOS_ACCESS_TOKEN` and `aos desktop-url` are deleted, not replaced.** On the box, scripts use `aos` over the Unix socket, which is unauthenticated by design and unchanged. `tools/e2e` signs in with a username and password like a real user, so the test exercises the real flow. Off-box scripted access is out of M6; a long-lived API token can be added later without disturbing any of this, and shipping a second credential type nobody has asked for is not worth it now.
9. **Rate limiting**: exponential backoff per account *and* per IP after 5 failures, capped around 15 minutes, counted **in memory** — a restart clearing the counter is an acceptable trade for not writing to SQLite on every failed guess. One uniform error message and a constant-time compare, so nothing leaks whether a username exists.
10. **The restricted first-login state cannot be extended**: first login issues a short access token carrying `must_change_password` and **no refresh token at all** until the reset succeeds.

### Consequences for the proto and the Desktop

- `AuthService` loses `CreateLoginCode` and `ExchangeLoginCode` and gains sign-in, refresh, logout, change-password and the ticket call. `internal/api/auth.go` loses `NewLoginCode`, `Exchange`, `sign`, `validSession`, `validToken` and `mediaLoad`.
- `desktop/src/api/client.ts` stops relying on `credentials: "same-origin"` and gains an interceptor that attaches the access token and refreshes on 401.
- `internal/proxy/proxy.go`'s `SessionCookie` constant and the rule that it is never forwarded to a Service both become dead — the cookie no longer exists. The CSP sandbox on forwarded pages stays.

## Correction from *The authentication screens and account lifecycle* (resolved 2026-09-17)

Decision 8 says `tools/e2e` "signs in with a username and password like a real user". It does not do so today: e2e drives `aos` inside the container over the Unix socket and never touches HTTP authentication. That sign-in test is therefore **new work, not a change**. Its only HTTP client is the forwarding check at `tools/e2e/m2_test.go:69`, which is unauthenticated and must be updated when forwarded Services move inside the session.

**And a scope note on decision 1.** It rejects "a path-scoped cookie" for the four browser-initiated Desktop loads, and that rejection stands: `/files/raw` and the WebSockets are the Desktop's own origin, where a cookie is an automatic credential on the API's own surface and the ticket works perfectly well. Forwarded Services are a different surface — an arbitrary third-party page whose sub-resources the Desktop does not control — where a ticket on the entry URL cannot cover what the page loads next. A cookie scoped to `Path=/port/<n>/` reaches one Service and no API endpoint, so the API stays header-only. The final consequence bullet is therefore half right: `SessionCookie` dies as *the Desktop's* credential, but the rule that a Service is never handed the Desktop's cookie survives and now applies to the forwarding cookie too.

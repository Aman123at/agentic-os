# The authentication model: JWT, refresh, and the WebSockets

Type: grilling
Status: open
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

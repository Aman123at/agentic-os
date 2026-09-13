---
status: accepted
---

# The aosd API always requires authentication, even on localhost

Binding to 127.0.0.1 does not stop a malicious website in the user's browser, another program on the Host, or a script an Agent runs inside the Machine from calling `localhost:7700` to start Tasks, change Autonomy, or approve its own Approvals. So `aosd` generates an access token on first start (stored where Agents cannot read it), the Desktop signs in once through a one-time link that becomes an HttpOnly, SameSite=Strict cookie, every request's Host and Origin are checked, the in-container CLI uses a Unix socket that rejects Agent-confined callers, and path-forwarded Service pages are served in a CSP sandbox so they cannot ride on the Desktop's sign-in. This reverses the earlier "no auth when local" decision.

## Considered Options

- **No auth on localhost, Origin/Host checks only**: rejected; blocks websites but not Host programs or scripts inside the Machine.

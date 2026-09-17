# The authentication screens and account lifecycle

Type: grilling
Status: resolved
Blocked by: 02

## Question

With the token mechanism settled, specify what the user actually sees and does.

1. **First run.** The Desktop shows a signup screen when no user exists — username and password, no email. What are the password rules, and does a weak password get refused or warned? What stops someone else reaching the box first and claiming the account (the "unclaimed instance" window)? On a loopback-only bind that window is small; on `0.0.0.0` it is a real race.
2. **Switching CLI → UI.** Aman said switching to UI for the first time requires signup. Where does that happen — in the browser on first load, or as a CLI step during `aos config set mode=ui`? A CLI path (`aos user create`) closes the unclaimed-instance window entirely.
3. **Login screen.** Where it sits relative to the Desktop shell — a separate route, or a gate rendered before the desktop mounts. What happens to an expired session mid-use: a modal over the desktop, or a bounce to login losing window state.
4. **Logout.** Where it lives in the UI (Apple menu? System Settings?), and that it revokes the refresh token server-side rather than only clearing the client.
5. **Password change.** Requires the current password. Does changing it invalidate other sessions? What is the CLI equivalent (`aos user passwd`) for someone locked out of the UI — this is the *only* recovery path, since there is no email reset.
6. **Recovery when the password is lost.** With no email and no OTP, the honest answer is a CLI reset run over SSH. Confirm that exists and is documented, or a forgotten password means a wiped database.
7. **Brute force.** A login endpoint behind nginx on the public internet needs rate limiting or lockout. Decide the shape and what happens to a locked-out legitimate user.
8. **Which Desktop surfaces change**: the boot sequence in `desktop/src`, the store's auth state, and how a 401 mid-session is handled centrally rather than per-call.

## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations").

### Three findings that reshaped the ticket

**Half the ticket was already answered, and one part was answered by nobody.** *Reach, bind and the two Host checks* killed the signup screen outright — the initial credentials come from `config.yml`, so the unclaimed-instance race of question 1 is closed by construction — and *The authentication model* fixed the brute-force shape of question 7. But question 2 was a **circular handoff**: ticket 01 passed "the account is only required in `ui` Mode, and the `cli` → `ui` switch triggers the initial-password requirement" to *Mode switching*, whose question 6 passes "switching to `ui` with no user account yet" back here. It is decided below.

**The forwarder sits outside the authenticator, and M6 removes the only thing hiding that.** `internal/daemon/daemon_linux.go:198` builds `proxy.New(d.auth.TCP(handler), Port)` — the proxy wraps the authenticator, so **forwarded Service traffic never reaches `Auth.TCP`**. It is unauthenticated today and safe only because `internal/proxy/proxy.go:44` refuses a non-localhost Host and Compose publishes on `127.0.0.1`. Ticket 01 deletes that guard and binds `0.0.0.0`, which would put every port an Agent opens — a dev server, an admin console, a database UI — on the public internet at `http://IP:7700/port/<n>/` with no password in the path. `tools/e2e/m2_test.go:69` is the proof: it fetches a Service page with no credential and expects 200.

**Long-lived connections outlive the token that authorised them.** The event stream is a held Connect stream authorised once at request start (`store.ts:211` → `events.ts:19`); `/ws/session/<id>` is authorised once by a 30-second ticket and then runs for hours. Nothing re-checks. So an expiring session interrupts nothing the user is watching, and a logout that only drops client tokens leaves a live terminal and a live event feed running for a browser that is supposedly signed out.

### Decisions

1. **The `cli` → `ui` switch creates the account, in the CLI, never in the browser.** Entering `ui` Mode requires `username` and `password` in `config.yml`; if `password` is unset the switch generates one, writes it back and prints it — the same rule ticket 01 set for first start, so there is one behaviour and not two. `aosd` hashes it into `users` when it starts in `ui` Mode with no user row. The browser therefore never has an unclaimed state for anyone to race for. This closes the handoff *Mode switching* (08) question 6 was waiting on.

2. **The login screen is the `needs-signin` boot phase promoted, not a new route.** `App.tsx:22` already has the phase and the boot card; the form replaces the `aos desktop-url` paragraph, on the same wallpaper gradient. `Shell` never mounts unauthenticated, so no app code ever has to handle a missing identity.

3. **Refresh is proactive, on a timer at ~80% of the 15 minutes — never reactive on the first 401.** `desktop/src/api/watch.ts:1-11` already documents why: plain HTTP means HTTP/1.1, six connections per origin across all tabs, each tab's event stream holds one and a Watch stream often a second, and "held streams leave RPCs, and even a reload, queued behind them". A refresh fired on the first 401 queues behind exactly those streams. `events.ts:22` and `watch.ts` must also tell `Unauthenticated` apart from a dropped stream, or an expired token becomes a reconnect storm instead of a prompt.

4. **An expired session is a modal over the desktop, not a bounce** — and only when the *refresh* fails, not when an access token does. `store.ts:797` keeps the window layout in `sessionStorage`, so a reload restores the windows; what a reload destroys is unsaved TextEdit buffers (the document lives in the CodeMirror view, `TextEdit.tsx:90-99`, and only the close button asks) and terminal scrollback. The shell dims, a sign-in modal takes the password and work resumes in place. **One exception**: a family-revoked rejection means the refresh token was replayed, so that path clears everything and hard-reloads.

5. **Logout revokes server-side and closes what the family authorised.** The refresh-family id is carried by the access token, by every issued ticket and by every held stream, so revoking a family **closes the event streams and session WebSockets it authorised**. Without that, "sign out" is decoration: the browser keeps a live terminal and a live feed. Home for it: a new **Account** pane in System Settings (who you are, change password, sign out), plus turning ◆ (`MenuBar.tsx:33`, a plain button that opens About — there is no dropdown anywhere in the shell) into a real three-item menu: About This Machine, System Settings…, Sign Out.

6. **Password rules: at least 12 characters, refused rather than warned.** Plus a refusal of the initial password and of the username itself. No composition rules and no strength meter — on a box scanners find within hours, length is the only rule that pays, and a warning is not a defence. Enforced on the server, echoed by the client so the refusal is not a round trip. Worth a sentence in the ADR: `crypto/pbkdf2` has no bcrypt-style 72-byte input cap, so a long passphrase is not silently truncated.

7. **Changing the password signs every other session out** — every refresh family except the one making the change, which stays signed in. The usual reason to change a password is the suspicion that it is known.

8. **The forced first change is a third boot phase (`must-change-password`), not a modal in the Shell.** The pre-reset token can do nothing else anyway (ticket 01). Same card, two fields, and a plain line saying the initial password came from `/etc/aos/config.yml` and is about to be blanked there. On success it receives its first refresh token and boots the Shell without a reload.

9. **Recovery is one verb: `sudo aos user passwd`, over SSH, and it is the only path.** No email, no reset link; root on the box is the authority, and the control socket is root-only anyway (*The systemd unit*, decision 7). It works locked out or not, and it clears that account's lockout counters — the person with root is not the brute-forcer. There is no `aos user create` (the config creates it) and no `aos user rename`: **`username` in `config.yml` is authoritative**, so changing it renames the single user at the next start, consistent with the file being the single source of truth. `password` is write-once-then-blanked and `aos config get` masks it the way it masks `api_key`. This fixes a gap in *The config file*, whose startup-only key list names `username` and not `password`.

10. **A locked-out user is told the uniform message plus the countdown**: "Too many attempts. Try again in 4 minutes." The remaining time leaks nothing an attacker cannot measure anyway, and without it the screen is unusable. Documented escapes: wait, `sudo aos daemon restart` (the counters are in memory by *The authentication model*'s decision 9), or `sudo aos user passwd`.

11. **Forwarded Services move inside the session.** The Desktop opens one at `http://IP:7700/port/<n>/?t=…` with the single-use ticket already designed, and `aosd` exchanges that ticket for a cookie **scoped to `Path=/port/<n>/`** — which is what lets the Service's own sub-resources load, since an arbitrary third-party page cannot attach an `Authorization` header. That is why the cookie existed in the first place. **Stated out loud rather than assumed:** this is admissible under ticket 01's "tokens live in headers, never cookies" rule, because that rule exists so the *API* carries no automatic credential, and this cookie reaches one forwarded Service and no API endpoint. The API stays header-only, so deleting the DNS-rebinding defence stays safe. `<port>.localhost` keeps working under Compose, where the Host really is localhost and the publish address is loopback. *The authentication model*'s decision 1 rejected a path-scoped cookie for the Desktop's own four loads and that rejection stands — `/files/raw` and the WebSockets are the API's own origin, where the ticket works; a forwarded page is a third party whose sub-resources the Desktop does not control. Considered and rejected as the M6 default: refusing any forwarded request that did not arrive from loopback and printing the `ssh -L` command — safe and free, but it costs remote Service access, which is most of the point of a VPS. It stays the fallback if decision 11 proves too much mechanism to land.

12. **Surfaces.** `App.tsx` (three phases), `store.ts` `boot`/`signIn`, `client.ts` (an interceptor; `credentials: "same-origin"` deleted), `events.ts` and `watch.ts` (Unauthenticated ≠ dropped), `term.ts:112` and `apps/browser/page.ts:101` (ticket on the socket URL), `apps/finder/fs.ts:114,125` and `PdfView` (ticket on raw loads and ranges), a new `settings/AccountPane.tsx`, `MenuBar.tsx`, and `AuthService` in `proto/aos/v1/services.proto`.

### A correction to *The authentication model*

Its consequence list says `tools/e2e` "signs in with a username and password like a real user". It does not: e2e drives `aos` inside the container over the Unix socket and never touches HTTP authentication. So that is a **new** test rather than a changed one. Its only HTTP client is the forwarding check at `tools/e2e/m2_test.go:69`, which is unauthenticated — and is therefore the test decision 11 breaks and must update.

### Owed to other tickets

- ***Mode switching and the single binary*** (08): question 6 is answered by decision 1 — the switch is the account-creation moment, it happens in the CLI, and it generates and prints a password when none is set.
- ***Which decisions become ADRs*** (15): ADR-0007's `## M6: passwords, JWT and a public bind` section gains two things it did not have — the **path-scoped forwarding cookie** with the argument for why it does not reopen what ticket 01 closed, and **family revocation closing live streams and sockets**. `internal/proxy/proxy.go:1` already cites ADR-0007, so this belongs in that ADR and nowhere else.
- ***The documentation site*** (13): two obligations. `sudo aos user passwd` is the *only* password recovery there is and must be documented as such, not buried; and `/port/<n>/` is now reached from the Desktop rather than typed, which the forwarding page has to say.

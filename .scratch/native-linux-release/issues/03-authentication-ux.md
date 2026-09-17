# The authentication screens and account lifecycle

Type: grilling
Status: open
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

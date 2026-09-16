# M4 review — Desktop apps

_2026-09-16. Written for the milestone review the working agreement (§22) calls
for. Everything below was re-run today on the macOS Host at commit
`6a228f1` (M4.8), with a clean tree._

## 1. The acceptance criteria

> **Accepted when:** every app works against the fake provider in Playwright, and
> the live suite passes in `ui` Mode.

Both hold.

**Fake-provider gate — `go run ./tools/ci`, all stages green.** The Playwright
stage is **52 passed**, with no skips and no retries (`retries: 0`, one worker,
serial). Every app in M4's list has at least one spec that drives it against a
real `aosd` in the `ui` image; the map is in §2.

**Live suite — `go run ./tools/ci live`, green end to end.** Run 2026-09-16
against the real provider (`gpt-5.6-terra`) with the key from `.env`: a real Task
reaches Done and leaves its file behind, the first visible Agent step is 43 ms
(target < 1 s), the eight-window drag on a real GPU renders 120 frames at p95
0.75 ms with 0 dropped, and the run reports its own spend ($0.0179). Total spend
across the M4.7/M4.8 sweep and all runs: about $0.09.

It is worth being precise about the order of events: the live suite **failed** on
its first run (first visible step 1,204 ms), which aborted the rest of that spec.
A manual Chrome sweep of every app on the same live Machine covered what the
suite never reached and turned up twenty defects — M4.8 in the plan. All twenty
are fixed, and the suite passes now. The live suite has **not** been re-run since
the wallpaper-spec fix below, because that fix is test-side only and the live
suite does not spend the key on anything it touches; say the word and I will
re-run it (roughly $0.02).

## 2. What is covered, app by app

| M4 deliverable | Where it lives | Specs that drive it |
|---|---|---|
| Agent app: live feed, Follow-ups, Audit Log, usage | `desktop/src/apps/agent/` | 4 (`agent.spec.ts`), plus `approval.spec.ts`, `sync.spec.ts` |
| TextEdit | `desktop/src/apps/textedit/` | 2 (`textedit.spec.ts`) — edit, ⌘S, overwrite conflict, and `open_in_desktop` |
| Preview | `desktop/src/apps/preview/` | 2 (`preview.spec.ts`) — image, PDF paging, video Range requests, zoom read-out |
| Activity Monitor (+ Services and ports) | `desktop/src/apps/activity/` | 1 (`activity.spec.ts`) — three of four tabs asserted |
| Software (Ledger, Checkpoints, Replay progress) | `desktop/src/apps/software/` | 1 (`software.spec.ts`) — Checkpoint round-trip; Packages and Replay only asserted to render |
| System Settings | `desktop/src/apps/settings/` | 6 (`settings.spec.ts`, `settings-behaviour.spec.ts`) — Autonomy, API key masking, Memory, shortcut remap, error wording |
| Trash | `desktop/src/apps/` + Dock | 2 (`trash.spec.ts`) — Put Back, empty, type icons, full-bin tile |
| Downloads stack | `desktop/src/shell/` | 1 (`downloads.spec.ts`) |
| `open_in_desktop` and `notify` Tools | `internal/agent/` | `textedit.spec.ts`, `notify.spec.ts` |
| Finder "Ask Agent…" and 🔒 Protect | `desktop/src/apps/Finder.tsx` | 9 (`finder.spec.ts`), including the built-in Protected Paths and ancestors |
| Shell/window manager regressions from M4.8 | `desktop/src/shell/`, `store.ts` | 8 (`layout.spec.ts`), 2 (`glass.spec.ts`), 2 (`sync.spec.ts`), 1 (`desktop.spec.ts`) |
| Terminal (M3, re-worked in M4.8) | `desktop/src/apps/Terminal.tsx` | 4 (`terminal.spec.ts`) |

## 3. §16 performance targets

| Target | Value | Measured today |
|---|---|---|
| `docker compose up` to usable | < 5 s | aosd answers in 0.3 s, Desktop paints in 0.7 s — asserted in `global-setup.ts` |
| Window drag | 60 fps, p95 frame < 16.7 ms | `perf.spec.ts`, green |
| Drag with eight windows | main-thread p95 cheap | `perf.spec.ts`, green; real-GPU run from the live suite: 0 dropped frames |
| Terminal keystroke echo | < 30 ms p95 | `perf.spec.ts`, green |
| **Tool dispatch overhead** | **< 10 ms p95** | **not measured anywhere** — see §5 |
| First visible Agent step | < 1 s | 43 ms (live suite) |
| `aosd` idle memory | < 50 MB RSS | 30.9 MB after startup, 40.7 MB after the whole suite — asserted |
| Image size | `cli` < 520 MB, `ui` < 540 MB, < 180 MB compressed | `cli` 510 MB / 154 MB, `ui` 513 MB / 155 MB |
| Desktop initial bundle | < 150 KB gzipped | 138 KB |

## 4. What this review changed

One thing. The full sweep run for this review failed a spec that had never been
run in a full sweep before: `desktop.spec.ts`, "right-clicking the desktop picks a
generated wallpaper that survives a reload". It moved the Hue slider by assigning
`el.value` and dispatching a synthetic `input` event, which React's value
tracking swallows, so the preview never repainted and the wallpaper kept its
default hue. **The app was correct; the spec was not driving the slider the way a
user does.** It now uses Playwright's `fill`, as every other spec in the suite
does. Verified alone, then in the full stage: 52/52.

That is worth noting for its own sake: a spec that only ever ran on its own can
be wrong in a way the suite will not show you.

## 5. Thin spots, carried into M5

Nothing here blocks M4, but none of it should be forgotten.

1. **Tool dispatch overhead (§16) is not measured.** There is no Go benchmark for
   policy + sandbox + framing around `true`, so the target is a claim rather than
   a gate. M5's first bullet ("all §16 targets enforced in CI") covers it.
2. **Defect 8.9 is reasoned, not observed.** The menu bar and Dock keeping the old
   theme does not reproduce under headless Chromium, and the Host it was seen on
   was not available to re-check. `glass.spec.ts` guards the repaint nudge, not
   the pixels. One look on the next real-Chrome pass settles it.
3. **Software is the shallowest app in the gate.** Checkpoints are exercised
   properly; the Install Ledger is only asserted to have a toolbar, and Replay
   only to render either progress or its empty note. Neither has been driven with
   real packages in the Desktop.
4. **Activity Monitor's fourth tab** (Logs) has no assertion.
5. **Never covered by any sweep** (needs a human, a real Service, or another
   Host): the Upload button's native file picker, Download… to the Host, a real
   Service with port forwarding, Checkpoints/Restore/Replay with real packages,
   Interrupt/Resume, Immersive mode, Safari path-forwarding mode, and the Windows
   and Linux Hosts. All of these are already in M5's scope.
6. **M3 has no ✅ in §18** while M0–M2 do. Its acceptance criteria are met and
   covered by specs (`layout.spec.ts`, `sync.spec.ts`, `perf.spec.ts`,
   `signin.spec.ts`, `terminal.spec.ts`, `finder.spec.ts`), so this looks like a
   missed mark rather than open work — worth confirming before M5.

## 6. Verdict

M4's acceptance criteria are met. The milestone is ready to be signed off, and
M5 — hardening and release — is the next piece of work, to be planned and
approved before any of it is written (§22).

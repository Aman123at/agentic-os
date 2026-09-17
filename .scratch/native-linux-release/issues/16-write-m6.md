# Write M6 into docs/PLAN.md

Type: task
Status: open
Blocked by: 01, 02, 03, 05, 06, 07, 08, 10, 11, 12, 13, 14, 15, 17

## Question

The destination. With every decision above settled, write the M6 section of `docs/PLAN.md` in the same voice and shape as M0–M5, and put it to Aman for approval.

It must:

1. Break into **numbered sub-tasks small enough to track one by one** — Aman asked for this explicitly, and M5.1/M5.2 are the model for the grain.
2. Give each sub-task its acceptance criterion and its tests, in the style of the existing "Tests:" paragraphs. Remember that this Mac cannot exercise a Linux VPS: the plan ships unit and logic tests, and Aman tests on real hardware and reports back.
3. Update the parts of the existing plan this effort invalidates — §2 Scope, §5 Repository layout, §6 Container and Compose, §7 Security model, §12 Services and port forwarding, §15 Cross-Host support, §16 Performance targets, §17 Testing strategy, §20 Decision log, and the §22 working agreement's Hosts row.
4. Reference the ADRs settled in *Which decisions become ADRs*.
5. State the acceptance criterion for M6 as a whole, in the form §18 already uses — something a fresh user on a fresh VPS can be measured against.
6. Note the order of work, including which sub-tasks must land before others (the Shared Folder removal and the filesystem widening both touch `Layout`).

Then stop. **No implementation code until Aman approves it.**

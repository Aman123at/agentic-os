# The glossary after Machine and Host collapse

Type: task
Status: open
Blocked by: —

## Question

`CONTEXT.md` defines **Machine** as "the Ubuntu environment that AOS runs" and **Host** as "the user's own computer on which the Machine runs, which may be macOS, Windows or Linux". On a VPS install those are the same computer, and a large amount of the product's language assumes they are not: "forwarding to the Host", `aos doctor --host-check`, the Shared Folder, "Cross-Host support" (§15), the Machine Profile.

This ticket is the writing itself, not a decision: bring the glossary back into line so that later tickets and M6 are written in words that still mean something.

Work to do:

1. Redefine **Machine** so it covers both a container and a VPS, and redefine or retire **Host**.
2. Decide and define the word for the two ways of running AOS. "Native install" and "Compose install" are the obvious candidates; something better may exist. Whatever it is, use it consistently from here on.
3. Delete the **Shared Folder** entry (coordinate with *Removing the Shared Folder*).
4. Add entries for the genuinely new concepts: the configuration file as the source of truth, the account/sign-in, the model catalogue.
5. Sweep `docs/PLAN.md` §15 "Cross-Host support" and §12 "Services and port forwarding" for language that no longer holds, and list what M6 must correct.

`CONTEXT.md` is a glossary and nothing else — no implementation detail belongs in it.

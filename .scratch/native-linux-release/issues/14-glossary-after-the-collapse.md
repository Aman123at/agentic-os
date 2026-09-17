# The glossary after Machine and Host collapse

Type: task
Status: resolved
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

## Input from *The systemd unit and the `aos service` lifecycle* (resolved 2026-09-17)

`aos daemon` (Agentic OS itself, one systemd unit) versus `aos service <name>` (a **Service**: a user's supervised program) is exactly the distinction the glossary must make unmissable, now that Machine and Host have collapsed and a real systemd exists on the box. `CONTEXT.md` currently lists "systemd unit" under _Avoid_ (`CONTEXT.md:23`), and `internal/agent/instructions.go:38` tells Agents "There is no systemd" — both need revisiting.

## Input from *Removing the Shared Folder* (resolved 2026-09-17)

The **Shared Folder** glossary entry (`CONTEXT.md:37`) is deleted, not rewritten. This ticket owns the replacement: a short **"Retired terms"** line at the foot of the glossary, naming words that appear in old commits, ADRs and docs but no longer exist in the product — Shared Folder first among them. Decide whether Host joins it or is redefined, since Machine and Host collapse rather than disappear.

## Input from *Mode switching and the single binary* (resolved 2026-09-17)

**Mode stops being a property of the image and becomes a runtime setting.** `CONTEXT.md`'s Mode entry and `docs/PLAN.md` §6.1 both describe the build-target version — one image per Mode, `AOS_IMAGE_MODE` baked in, `AOS_MODE=ui` refused on a `cli` image. All of that is deleted. The replacement definition: one binary, one image, `mode:` in `config.yml`, and `cli` Mode means **no TCP listener** rather than "the Desktop is not built in".

## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations"). The glossary itself is **written**: `CONTEXT.md` is rewritten in this commit. What follows records why, and what the sweep owes to *Write M6 into docs/PLAN.md*.

### Findings that reshaped the ticket

**Host does not collapse. It splits, and the surviving half is a client.** The ticket's premise — "on a VPS install those are the same computer" — is true of the *runtime* relationship only. Four live surfaces read Host as the computer the **browser** runs on, and none of them die on a VPS: `desktop/src/shell/keyboard.ts:13` (`hostOS()` picks the shortcut defaults), `desktop/src/apps/settings/AppearancePane.tsx:12` ("Auto — Follow the Host"), `desktop/src/apps/settings/KeyboardPane.tsx:115` ("so they reach AOS instead of the Host"), and `desktop/src/apps/Finder.tsx:237` (`downloadToHost`, where the file leaves the server for the user's laptop). So the question was never "collapse or keep"; it was which half the word follows.

**Whichever half it follows, the word becomes a lie.** Natively the Host hosts nothing. A silent redefinition is worse than a retirement: `aos doctor --host-check`, PLAN §15 "Cross-Host support" and ADRs 0003–0008 keep the spelling while meaning the dead sense, and no reader is warned.

**`Install` cannot be the term for the two shapes.** The obvious answer to work item 2 — a capitalised **Install** with *native* and *Compose* kinds — collides with **Install Ledger**, `install_package` and the Agent prompt's own "Installs you make yourself in your Session". Three existing uses of the word, all meaning software inside the Machine.

**The Machine Profile carries the same falsified sentences as the Agent prompt, and only one line of it was booked.** `internal/profile/profile.go:86-88` tells every Agent that Services are reachable at `http://<port>.localhost:<AOS port>` (ticket 01 made `/port/<n>/` canonical precisely because `*.localhost` cannot resolve from a remote browser), that `~/Shared` exists (booked by ticket 07), and that "outside the home folder, only software from install_package and /etc changes by root survive a restart" — the behaviour-shaping falsehood ticket 08 found in the prompt, in a second file nobody checked.

**PLAN §12's low-ports bullet is a container fact stated as a product fact.** M0.7 measured `ip_unprivileged_port_start=0` *inside Docker*, which sets it; the kernel default is 1024. So §12's "nginx on port 80 works as `aos`" and the prompt's "ports above 1024 are simplest, but any port works" (`internal/agent/instructions.go:38`) are both wrong on a stock Ubuntu server. Not measurable from this Mac: `cat /proc/sys/net/ipv4/ip_unprivileged_port_start` on the VPS settles it.

**Natively, a Service is on the internet directly — and nothing in the effort had booked it.** Every exposure decision so far argues about port 7700 and the forwarder. Under Docker an unpublished port is unreachable: the network namespace is the barrier. Natively there is none, so an Agent that starts a dev server on `0.0.0.0:3000` has published it, bypassing the forwarder, the Account and the whole of tickets 01/02/03 — and the Agent prompt actively encourages starting servers. `internal/service/ports.go:144` already has a `wildcard()` helper, so AOS can see it and currently says nothing.

**Three entries besides Shared Folder were falsified, plus the file's own opening line.** **Replay** ("when the Machine starts" — off natively), **Install Ledger** ("the authority on what software the Machine should have after a restart" — true only where Replay runs), and **Service**'s `_Avoid_` list banning "systemd unit" at exactly the moment a real systemd appears and a Service genuinely is not one. `CONTEXT.md:3` opened with "A personal, **containerised** Ubuntu environment".

### Decisions

1. **Host is retired, not redefined.** Its two jobs split: the computer AOS runs on is the **Machine**; the computer the user browses from is **your computer** — lowercase, undefined, because nothing about it is ours to decide. AOS only ever learns which keyboard shortcuts it prefers and where a download should land.

2. **The two shapes are fixed lowercase phrases, *native install* and *Compose install*,** defined as one glossary entry rather than promoted to a capitalised term. The collision above rules out `Install`, and inventing *Deployment* or *Edition* adds a proper noun for a distinction that lives mostly in prose. The effort had already converged on this vocabulary by itself (19 "natively", 9 "native install" against 1 "Compose install").

3. **Machine covers both:** *"The Ubuntu system that AOS runs on and that Agents act upon, including its filesystem, processes and installed software: a whole Linux server on a native install, a container on a Compose install."* `_Avoid_` keeps **container** — saying "the container" for the Machine is still wrong half the time — and gains **server** and **VPS**.

4. **Four entries added: Account, Configuration, Model Catalogue and Daemon.** The first three are work item 4. **Daemon** is the one the ticket did not ask for and ticket 10 demanded: the AOS program itself, started by systemd and controlled by `aos daemon`, *"It is not a Service"* — with Service redefined as *"a program the user asked AOS to keep running … a Service is not a systemd unit, and systemd knows only about the Daemon."* That pair is what makes `aos daemon` versus `aos service <name>` unmissable, and it moves "systemd unit" out of the `_Avoid_` list and into the definition.

5. **Entries deleted or amended:** **Shared Folder** deleted. **Mode** rewritten (a key in the Configuration, changed with `aos mode`; `cli` means *nothing listening on the network*, not "no Desktop built in"; one binary carries both). **Replay** and **Install Ledger** qualified with "on a Compose install". **Machine Profile** drops "key folders" — there are a great many now. The opening paragraph loses "containerised" and says the Desktop is watched "in the browser", with the two shapes named in one sentence.

6. **A "Retired terms" section at the foot, with a scope rule.** It names **Shared Folder** and **Host**, says where each still appears (old commits, ADRs 0003–0008, `docs/m0-findings.md`) and what replaced it. The rule: *retirement binds product language — the glossary, the documentation, the Desktop's own words, the Agent prompt and the Machine Profile. It does not bind identifiers in code that mean something else.* So `internal/proxy`'s Host checks and every `http` `Host` header keep their spelling, and `agent.Host` — our own interface, meaning the Task an Agent reports to — is left alone rather than renamed across eight call sites for no user-visible gain. One exception is recorded rather than decided: **`aos doctor --host-check` is user-visible and cannot survive the retirement**, which belongs to the still-open "what `aos doctor` means without a container".

7. **PLAN §15 is replaced, not amended** — the title retires with the word. Five of its nine rows (Runtime, Landlock, both Shared Folder rows, Start command) describe Docker Desktop on a laptop and die or move to §6; three are genuinely client-side and survive (Service subdomains, Reserved shortcuts, Verified by).

8. **PLAN §12 gets a five-item correction list**, carried to *Write M6* below.

### Owed to other tickets

- ***Write M6 into docs/PLAN.md*** (16): the PLAN sweep — new §15, the §12 corrections, §1/§2's Docker framing — plus three code sub-tasks the sweep found: the Machine Profile's three lines, the Desktop's four Host surfaces, and the wildcard-listener warning.
- ***The documentation site*** (13, resolved): nothing owed. Its fifteen-page map already speaks of a native install and never uses Host.
- Open map item ***What `aos doctor` means without a container***: `--host-check` and `tools/hostcheck` carry a retired word; whatever replaces the check-list must not keep the spelling.

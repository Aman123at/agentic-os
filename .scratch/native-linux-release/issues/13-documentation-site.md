# The documentation site

Type: prototype
Status: resolved
Blocked by: —

## Question

A separate, deployable setup walkthrough covering install → configure → every command, which Aman will link from his own domain. Settled: Astro Starlight, in this repo, built to static files, never served by `aosd`.

Scaffold enough of it to judge, then settle:

1. **Where it lives** — `docs-site/` at the repo root — and that its `node_modules` and build output stay out of the Go build and out of `tools/ci`'s existing stages.
2. **The page map.** At minimum: what AOS is; install (native and Compose side by side, with their different filesystem reach stated plainly); the configure phase and the `config.yml` reference; **the restart-semantics warning Aman asked for**, stated where a first-time user meets it; putting nginx in front, with and without TLS; first sign-in and the account; the full `aos` command reference; models and efforts; enabling the Browser; upgrading and uninstalling; troubleshooting.
3. **How the command reference stays true.** A hand-written page will drift from Cobra's actual help within one release. Generating it from the command tree is the alternative — decide, because it changes the scaffold.
4. **Build and deploy.** Static output Aman uploads, GitHub Pages from Actions, or Netlify. Where the canonical URL lives.
5. **Versioning.** Docs for the released version versus `main`. Starlight supports this; decide whether it is needed yet.
6. **Whether `install.sh` is served from this site**, since Aman's plan is `https://amantiwari.co.in/agent-os/install.sh` — if so, the site's deployment and the release workflow are coupled and the script must be versioned alongside.

Link the scaffold from this ticket.

## Input from *install.sh, written and read* (resolved 2026-09-17)

- The Install page shows **both** invocation forms: `curl -fsSL … | sudo sh` first, the download-inspect-run form directly beneath it.
- A page must state plainly that **the Desktop password is root on this server** — the `aos` user holds `NOPASSWD:ALL` and `bind: 0.0.0.0` puts the sign-in screen on the internet.
- The documented path is **`/agentic-os/`**, not `/agent-os/`. The canonical script URL is `raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh`; the domain redirects to it, so a docs deploy can never break installs.
- The commented `config.yml` template is the first documentation surface a user meets. Keep it and the docs saying the same thing about write-back, the generated password and the forced reset.

## Input from *The authentication screens and account lifecycle* (resolved 2026-09-17)

- **`sudo aos user passwd` is the only password recovery that exists.** No email, no reset link. It needs a page of its own, not a footnote — a user who loses the password and cannot find this has lost the Machine.
- **Port forwarding is now reached from the Desktop, not typed.** `/port/<n>/` carries a ticket, so a hand-typed URL will refuse. The forwarding page has to say that and show where in the Desktop the link comes from.
- The sign-in page states the **12-character minimum** and the forced first change; the docs should not contradict it.

## Input from *Installing Chromium lazily, and Compose parity* (resolved 2026-09-17)

The Browser page stops describing a build flag and describes a command: `sudo aos browser install`, which downloads about 120 MB and needs about 1 GB free. It must state the supported distributions Chrome for Testing actually publishes for — ubuntu22.04, ubuntu24.04, ubuntu26.04, debian12, debian13; **not** ubuntu18.04 or ubuntu20.04 — and that the same command is how Compose users get the browser, because the published image no longer carries it.

## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations").

**Prototype**: [`tools/spikes/docs-site/`](../../../tools/spikes/docs-site/) — Astro 7 + Starlight 0.42, the full page map as files, two pages written out, and a working reference generator run against the real command tree. Throwaway, marked so in every file; M6 writes the real one at `docs-site/`.

```sh
cd tools/spikes/docs-site && npm install && npm run refgen && npm run build
```

37 pages, 2.1 MB of static output, 213 MB of gitignored `node_modules`.

### Four findings that reshaped the ticket

**The install command this site documents cannot work, and it is not the site's fault.** Every hosting decision already settled — `raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh` as canonical (11, decision 8), `/releases/latest/download/<asset>` with no API call (09, finding 2) — assumes a public repository. It is private. Measured:

```
raw=404   https://raw.githubusercontent.com/Aman123at/agentic-os/main/README.md
rel=404   https://github.com/Aman123at/agentic-os/releases/latest/download/agentic-os-linux-amd64.tar.gz
```

Not a rate limit and not a redirect: private assets are not served anonymously at all. Ticket 17 recorded *private* as a fact and nobody carried it forward. So the first page of the documentation, the installer and the release stage all depend on one decision nobody had booked.

**The same decision governs the deploy.** GitHub's own page: Pages is available "in public repositories with GitHub Free … and in public and private repositories with GitHub Pro, GitHub Team, …". One of question 4's three options is conditional on finding 1, not independent of it.

**Starlight has no built-in versioning.** Question 5 asserts it does. It does not — versioning is the third-party `starlight-versions` plugin (0.10.1, community-maintained, tracking Starlight's own majors). So "decide whether it is needed yet" was not a config flag; it was a dependency on someone else's release cadence.

**`README.md` is the documentation surface nobody booked.** Tickets 07 and 12 amend it line by line (`README.md:22-26`, `README.md:16`), but its *thesis* is falsified: "An Ubuntu Machine in Docker", a Quick start that is `docker compose up --build`, `INCLUDE_BROWSER=true`, `AOS_MODE=cli`, and a Troubleshooting section entirely about a Shared Folder that ticket 07 deletes. Twenty-eight lines, of which about twenty are wrong after M6 — and the moment finding 1 is acted on, it is the first thing a stranger reads.

### Decisions

1. **The repository goes public at the first tag**, as its own M6 sub-task ordered before the release stage ever runs. The documentation is written as if it is already public and hedges nothing. Until then the site's install command is unrunnable, which is expected rather than a bug to route around. Everything downstream — ticket 11's canonical URL, ticket 09's asset path, question 4's second option — is unblocked by this and by nothing else.

2. **It lives at `docs-site/` at the repository root**, beside `desktop/`. `.gitignore` gains `docs-site/node_modules/`, `docs-site/dist/` and `docs-site/.astro/`; the lockfile is committed. No existing `tools/ci` stage sees it — `lint` is `gofmt` plus `go vet ./...`, and `ui()`/`npmInstall()` are hard-coded to `desktop`.

3. **The new checking splits the way ticket 12 split the browser: a cheap check always, an expensive one on demand.** The reference-drift check is pure Go and runs inside `lint` on every sweep; the Astro build becomes an **optional `docs` stage**, named explicitly like `live`, because it needs a second `node_modules` that the default sweep has no reason to install.

4. **The command reference is generated, by us, and not by `cobra/doc`.** Three reasons, all checked: `cobra/doc` pulls `go-md2man` and `blackfriday` into the module graph (`go.sum` carries both today as `/go.mod`-only hashes, so neither is built) and ADR-0002 is proud of that graph; its output carries `### SEE ALSO` boilerplate and an auto-generated footer; and it cannot emit Starlight frontmatter. Ours is 120 lines over `cmd.Commands()`. It needs exactly one production change — `rootCmd()` at `internal/cli/cli.go:40` becomes exported `Root()`. Output is committed under `docs-site/src/content/docs/reference/`, and `lint` regenerates into a temp directory and diffs, failing the way `gofmt -l` already does.

   **Stated out loud so it does not rot:** the generator gives `Short`, `Long`, flags and args, and can never give "when you would use this". Guides stay hand-written and link *into* the reference. If that line blurs, the site becomes a man page with a sidebar. A second consequence worth having: `aos --help` text is now a published surface, so thin `Short` strings are a documentation defect rather than a CLI nicety.

5. **Canonical URL `https://amantiwari.co.in/agentic-os/`, static output Aman uploads** — which is what the map already settled. `base: "/agentic-os/"` in `astro.config.mjs` is not cosmetic: with the default base, every asset URL and internal link the build emits is root-absolute and 404s once uploaded, and it fails **only on the real host** — `astro dev` and `astro preview` both serve it correctly. The check is `grep href=\"/ dist/index.html`, not a local preview. Netlify stays the named fallback if hand-uploading gets old; it deploys from private repositories, where Pages does not.

6. **`install.sh` is not served from this site.** Ticket 11 already settled it and the reason is worth keeping: the docs deploy and the release are decoupled, so a broken site cannot break installs. One hazard falls out that nobody had named — `amantiwari.co.in/agentic-os/install.sh` is a *redirect*, and if the site ever ships a file at that path the build silently shadows it and `curl … | sudo sh` starts receiving a web page. **The site must never contain `install.sh`**, and that sentence lives in a comment in `astro.config.mjs` where the person adding pages will meet it.

7. **No versioning for v0.1.0.** One site, one version, no plugin. Instead the site documents the *released* version and is deployed when a tag is cut rather than on every push to `main`, so it can never describe a command that is not in anyone's binary; a line in the header says which version it documents. Revisit when two releases are in use at once — which, since `install.sh` upgrades in place, may be never.

8. **The page map**, with each page's inherited obligation named. Every one of these exists as a file in the prototype:

   | Page | Carries |
   | --- | --- |
   | What Agentic OS is | the two shapes, and their different filesystem reach stated plainly |
   | Install on a server | both invocation forms, `curl \| sudo sh` first, download-inspect-run beneath |
   | Run it with Docker Compose | the confined alternative; the Shared Folder is gone; `<port>.localhost` still works here |
   | **What the password protects** | *"The Desktop password is root on this server"* — `NOPASSWD:ALL` plus `bind: 0.0.0.0`, its own page **and** stated inline on the install page |
   | Sign in and set your password | twelve characters, refused not warned; the forced first change; the lockout message |
   | Configuration | `/etc/aos/config.yml` key by key; write-back; *(pending restart)*; an unknown key refuses the start |
   | Models and reasoning effort | the catalogue as editable data |
   | Putting nginx in front | with and without TLS, and `bind: 127.0.0.1` afterwards |
   | Services and ports | `/port/<n>/` is opened **from the Desktop**, never typed — a hand-typed URL refuses |
   | The Browser | `sudo aos browser install`, ~120 MB, ~1 GB free, ubuntu22.04/24.04/26.04 · debian12/13, same command under Compose |
   | Restarts and work in flight | queued survive, running and awaiting-user are interrupted, Approvals denied |
   | **If you lose the password** | `sudo aos user passwd`, its own page, because a user who cannot find it has lost the machine |
   | Upgrading and uninstalling | re-running the installer is the upgrade; `--purge` |
   | Command reference | generated |
   | Troubleshooting | `aos status`, `aos doctor`, `journalctl -u aos` |

   **The restart warning Aman asked for by name goes on the Configuration page as its inversion.** Ticket 05 made the original false; the docs say the opposite in the same place the user would have looked for the warning.

9. **Rewriting `README.md` whole is an M6 sub-task owned by this ticket**, not two line-edits inherited from tickets 07 and 12. Native install first, Compose second, a link to the site, and the password-is-root sentence. It is the front door the moment decision 1 lands, and the one page that ships *inside* the repository rather than on Aman's domain.

### What the prototype had to work around, and what M6 does instead

`tools/ci lint` runs `go vet ./...` over the whole tree, so a spike program calling `cli.Root()` — which does not exist yet — would break the sweep. The sources are therefore `*.go.txt`, copied into `.refgen-spike/` and run from there, with `refgen/export_shim.go.txt` mapped into package `cli` by `go run -overlay`. The prototype walks the real command tree without editing `internal/cli` before approval, and `git status` stays clean. M6 does it honestly: export `Root`, put the generator at `tools/docsgen`, commit its output, diff in `lint`.

Two sizing notes from running it. The generator produces **22 pages against today's tree**, before M6 adds `daemon`, `config`, `mode`, `model`, `user`, `browser`, `status` and `uninstall` and deletes `desktop-url` — which is the case for generating rather than writing, made concrete. And the reference can only be generated once those commands exist, so the docs sub-task lands **late** in M6, after the CLI work.

### Owed to other tickets

- ***Release engineering*** (09): the repository must be public before the first tag, or every documented download 404s — measured, not assumed. The docs site deploys on tag, which is a second consumer of the release stage.
- ***install.sh, written and read*** (11): decision 8 stands, and now depends on decision 1 above. Plus the shadowing hazard: nothing the docs site publishes may sit at `/agentic-os/install.sh`.
- ***Write M6 into docs/PLAN.md*** (16): five sub-tasks, one of them ordered.

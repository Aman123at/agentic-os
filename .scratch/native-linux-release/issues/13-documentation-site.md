# The documentation site

Type: prototype
Status: open
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

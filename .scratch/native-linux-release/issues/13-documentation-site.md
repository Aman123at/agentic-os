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

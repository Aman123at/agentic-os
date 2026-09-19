# Documentation site

The Agentic OS docs — install, configure, operate, and every `aos` command.
Astro + [Starlight](https://starlight.astro.build/). One version, built and
deployed when a tag is cut, so the site can never describe a command that is not
in anyone's binary.

```sh
cd docs-site
npm ci               # ~270 packages, gitignored
npm run dev          # http://localhost:4321/agentic-os/
npm run build        # -> dist/
```

## The sub-path base is load-bearing

The site is served from a sub-path of Aman's own domain
(`https://amantiwari.co.in/agentic-os/`), so `base: "/agentic-os/"` in
`astro.config.mjs` is not cosmetic: with the default `/`, every asset URL and
internal link the build emits is root-absolute and 404s once uploaded — and it
fails *only there*, never in `astro dev` or `astro preview`. `go run ./tools/ci
docs` builds the site and asserts every href and asset in `dist/index.html`
starts with `/agentic-os/`, so the mistake cannot ship.

**Nothing here may ever be published at `/agentic-os/install.sh`.** That path is
a redirect to `raw.githubusercontent.com`; a file here would shadow it and start
serving a web page to `curl | sudo sh`. There is a comment saying so in
`astro.config.mjs`.

## The command reference is generated

The `reference/` pages are generated from the real `aos` command tree — one page
per top-level command plus an index — not written by hand. Do not edit them;
regenerate instead. The generator reads `Short`, `Long`, `Use`, flags and
subcommands; it *cannot* write "when you would use this" — that stays
hand-written in the guides, which link into the reference. If that line blurs,
the site becomes a man page with a sidebar. The generator ships at
`tools/docsgen`, where a `lint` drift check keeps these pages honest.

The guides under `start/`, `configure/`, `use/` and `operate/` are hand-written.

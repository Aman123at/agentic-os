# Documentation site

The Agentic OS docs — install, configure, operate, and every `aos` command.
Astro + [Starlight](https://starlight.astro.build/). One version, built and
deployed when a tag is cut, so the site can never describe a command that is not
in anyone's binary.

```sh
cd docs-site
npm ci               # ~270 packages, gitignored
npm run dev          # http://localhost:4321/
npm run build        # -> dist/
```

## The root base is load-bearing

The site is served at the root of its own subdomain
(`https://agenticos.amantiwari.co.in/`, a GitHub Pages custom domain kept in
`public/CNAME`), so `base: "/"` in `astro.config.mjs` is not cosmetic: a non-root
base prefixes every asset URL and internal link with a path the subdomain does
not have, so the whole site 503s and loads unstyled — and it fails *only there*,
never in `astro dev` or `astro preview`. `go run ./tools/ci docs` builds the site
and asserts no href or asset in `dist/index.html` carries a stale non-root base,
so the mistake cannot ship.

**Nothing here may ever be published at `/install.sh`.** That path is a redirect
to `raw.githubusercontent.com`; a file here would shadow it and start serving a
web page to `curl | sudo sh`. There is a comment saying so in
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

## The look

The theme lives in `src/styles/custom.css` (colour tokens for light and dark,
the header, sidebar, cards, code blocks and the splash landing page), with the
logo in `src/assets/logo.svg` and the favicon in `public/favicon.svg`. The
landing page is `src/content/docs/index.mdx` (`template: splash`). Fonts are
Inter and JetBrains Mono from Google Fonts, falling back to the system stack.

Use Starlight's built-in components (`Steps`, `Tabs`, `Card`, `LinkCard`,
`FileTree`) in `.mdx` guides rather than custom HTML, so pages stay consistent.
Every command a guide shows must exist in the binary: check it against the
generated `reference/` pages before you write it down.

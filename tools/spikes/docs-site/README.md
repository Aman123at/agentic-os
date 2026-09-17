# Spike: the documentation site

**Throwaway.** Written for [13-documentation-site.md](../../../.scratch/native-linux-release/issues/13-documentation-site.md)
so the site's shape could be argued over concretely. M6 writes the real one at
`docs-site/`; this exists to be read and shot at.

## Run it

```sh
cd tools/spikes/docs-site
npm install          # ~270 packages, ~213 MB, gitignored
npm run refgen       # regenerate src/content/docs/reference from the aos command tree
npm run build        # -> dist/, 37 pages, 2.1 MB
npm run dev          # http://localhost:4321/agentic-os/
```

`npm run dev` and `npm run preview` are the two places the base path *cannot* go wrong. Check
`dist/index.html` instead — every link and asset in it must start with `/agentic-os/`.

## What it settles

- **Astro 7 + Starlight 0.42**, `base: "/agentic-os/"`. On a sub-path deploy the default base
  emits root-absolute URLs that 404 only on the real host, never locally.
- **The page map** is the sidebar in `astro.config.mjs`. Every page exists; two are written in
  full (`start/install.md` and `start/password-is-root.md`) so the voice is judgeable rather than
  described, and the rest carry a "Must carry" paragraph naming the obligation each one inherited
  from a resolved ticket.
- **The command reference is generated**, not written — `refgen/`, 120 lines, walking the real
  cobra tree. 22 pages today, before M6 adds `daemon`, `config`, `mode`, `model`, `user`,
  `browser`, `status` and `uninstall`.
- **Nothing here may ever be published at `/agentic-os/install.sh`.** That path is a redirect to
  `raw.githubusercontent.com`; a file here would shadow it and start serving a web page to
  `curl | sudo sh`. There is a comment saying so in `astro.config.mjs`.

## What the generator does, and does not

It reads `Short`, `Long`, `Use`, flags and subcommands, and writes one page per top-level command
plus an index. It **cannot** write "when you would use this" — that stays hand-written in the
guides, which link into the reference. If that line blurs, the site becomes a man page with a
sidebar.

It deliberately does not import `github.com/spf13/cobra/doc`: that package pulls `go-md2man` and
`blackfriday` into the module graph (`go.sum` carries both today as `/go.mod`-only hashes, so
neither is built), emits `### SEE ALSO` boilerplate, and cannot write Starlight frontmatter.

### Why the sources are `.go.txt`

`tools/ci lint` runs `go vet ./...` over the whole tree, and this program calls `cli.Root()`,
which does not exist yet. So the sources are copied into `.refgen-spike/` and run from there, and
`refgen/export_shim.go.txt` is mapped into package `cli` with `go run -overlay` — the prototype
walks the real command tree without editing `internal/cli` before M6 is approved, and
`git status` stays clean.

M6 does it honestly: rename `rootCmd` to `Root` at `internal/cli/cli.go:40`, put the generator at
`tools/docsgen`, commit its output, and have `lint` regenerate into a temp directory and diff —
the way `gofmt -l` already works.

## Known, and left alone

The install page documents `https://amantiwari.co.in/agentic-os/install.sh`, which redirects to
`raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh`. **That URL 404s today, because
the repository is private** — as does `/releases/latest/download/…`, which `install.sh` needs.
Both were measured. The docs are written as if the repository is public, because M6 makes it
public before the first tag.

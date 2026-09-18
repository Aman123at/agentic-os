# Naming: module path, repo, binary and Docker Hub namespace

Type: grilling
Status: resolved
Blocked by: —

## Question

Surfaced by *Release engineering: multi-arch binaries and GitHub Releases*, which correctly refused to guess. Four names disagree, and the release workflow, `install.sh` and the docs site all hardcode them:

- Go module path: `github.com/amantiwari/agentic-os`
- Target GitHub repo: `Aman123at/agent-os` (does not exist as a remote here yet)
- Product name in the docs and glossary: "Agentic OS"
- Binaries: `aos` and `aosd`
- Docker Hub namespace: undecided
- Install URL: `https://amantiwari.co.in/agent-os/install.sh`

Settle:

1. **Is the repo `agent-os` or `agentic-os`?** The module path and the target repo disagree today. Renaming the module is a mechanical but wide change (every import in the tree); keeping a module path that does not match its repo is legal in Go but confusing, and `go install` from the repo URL will not work.
2. **The release asset names.** `agent-os-linux-amd64.tar.gz` versus `aos-linux-amd64.tar.gz` — and note the research finding that they must be **version-less** for `/releases/latest/download/` to resolve without the GitHub API.
3. **The Docker Hub repository**, and whether the image keeps the name `agentic-os` it has in `compose.yaml` today.
4. **The product name as the user meets it** — the docs site title, `aos --help`, the Desktop's About window. One name, used everywhere.
5. Whether the install URL path (`/agent-os/`) should match whatever is chosen, since it is the first thing a user types.

Cheap to decide now, expensive once a tag is published and people have installed from it.


## Answer

Settled 2026-09-17 by Aman ("continue with your recommendations") and by creating the repository.

**One name: `agentic-os`.** It was already the repo segment of the Go module path and the image name in `compose.yaml`, so it is the choice that moves the fewest things.

1. **The repo is [`Aman123at/agentic-os`](https://github.com/Aman123at/agentic-os)** — private, created and pushed this session with five commits (the M5.1/M5.2/M5.3 work, plus this charting). `origin` is set over SSH.
2. **Release assets**: `agentic-os-linux-amd64.tar.gz` and `agentic-os-linux-arm64.tar.gz`, plus `SHA256SUMS`. **Version-less**, per *Release engineering* — that is what makes `/releases/latest/download/<asset>` resolve without touching the GitHub API or its per-IP rate limit.
3. **Docker Hub**: `aman123at/agentic-os` (Docker Hub namespaces are lowercase). `compose.yaml`'s image name does not change.
4. **Binaries stay `aos` and `aosd`**; the product name the user meets stays **"Agentic OS"** — docs site title, `aos --help`, the Desktop's About window.

### Two loose ends handed back to Aman

- **The module path's owner segment is wrong.** *(Done 2026-09-18, commit `e9d7a5b`: renamed `amantiwari` → `Aman123at` tree-wide, its own commit ahead of the first tag.)* `go.mod` says `github.com/amantiwari/agentic-os`; the repo is under `Aman123at`. Legal in Go, but `go install github.com/Aman123at/agentic-os/cmd/aosd@latest` will not work, and it reads as a mistake. The fix is mechanical and tree-wide (every import). **It wants its own commit before the first tag**, not folding into M6 — recorded in the map's fog.
- **The install URL still says `/agent-os/`.** Aman specified `https://amantiwari.co.in/agent-os/install.sh`. With the name settled as `agentic-os`, that path is the one thing left that disagrees. Either move it to `/agentic-os/install.sh`, or keep `/agent-os/` deliberately as a shorter vanity path and say so in the docs. It is on his own domain, so it is his call — it does not block *install.sh, written and read*, which only needs the path to be fixed and known.

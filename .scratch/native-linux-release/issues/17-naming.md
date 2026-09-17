# Naming: module path, repo, binary and Docker Hub namespace

Type: grilling
Status: open
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

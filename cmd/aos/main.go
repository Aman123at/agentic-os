// Command aos is the CLI inside the Machine (PLAN.md §4.2). The image ships it
// as a link to aosd, which runs the same code; this command builds it alone.
package main

import (
	"os"

	"github.com/Aman123at/agentic-os/internal/cli"
	"github.com/Aman123at/agentic-os/internal/sandbox"
)

func main() {
	sandbox.RunHelperIfRequested()
	os.Exit(cli.Main())
}

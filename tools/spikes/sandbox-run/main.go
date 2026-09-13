// Command sandbox-run is an M0 spike tool: it runs a command as uid 1000 under a
// Ruleset planned from flags, so alternative Landlock layouts can be probed from
// a shell inside the Machine image.
//
//	sandbox-run -hidden /run/secrets -protected ~/.ssh -writable /home/aos -- bash -c '…'
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/amantiwari/agentic-os/internal/sandbox"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	sandbox.RunHelperIfRequested()
	var p sandbox.Policy
	flag.Var((*list)(&p.Hidden), "hidden", "Hidden path (repeatable)")
	flag.Var((*list)(&p.Protected), "protected", "Protected path (repeatable)")
	flag.Var((*list)(&p.Writable), "writable", "Writable tree (repeatable)")
	verbose := flag.Bool("v", false, "print the grants")
	flag.Parse()

	rs, err := sandbox.Plan(p, sandbox.RootFS())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	if *verbose {
		for _, g := range rs.Grants {
			fmt.Fprintf(os.Stderr, "%-8s %s\n", g.Access, g.Path)
		}
	}
	env := []string{"HOME=/home/aos", "PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd, err := sandbox.Command(rs, 1000, 1000, env, flag.Args()...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
}

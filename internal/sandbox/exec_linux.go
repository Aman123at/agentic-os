package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// helperEnv carries the Ruleset from the parent to the re-executed helper. The
// helper removes it before executing the confined program.
const helperEnv = "AOS_SANDBOX_RULESET"

type helperSpec struct {
	Grants []Grant
	Argv   []string
}

// Command returns a command that runs argv as uid/gid, confined to rs, with
// exactly env as its environment (aosd's own environment is never inherited).
//
// Confinement must not touch the calling process (aosd stays unconfined), so the
// command re-executes the current binary, which enforces rs on itself and then
// executes argv. Every program that calls Command must call RunHelperIfRequested
// at the start of main.
func Command(rs Ruleset, uid, gid uint32, env []string, argv ...string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	spec, err := json.Marshal(helperSpec{Grants: rs.Grants, Argv: argv})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(self)
	cmd.Env = append(append([]string{}, env...), helperEnv+"="+string(spec))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}},
	}
	return cmd, nil
}

// RunHelperIfRequested turns this process into the sandbox helper when it was
// started by Command. It returns only when the process is not a helper.
func RunHelperIfRequested() {
	raw, ok := os.LookupEnv(helperEnv)
	if !ok {
		return
	}
	_ = os.Unsetenv(helperEnv)
	var spec helperSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil || len(spec.Argv) == 0 {
		fmt.Fprintln(os.Stderr, "aos sandbox: invalid helper spec")
		os.Exit(126)
	}
	if err := Enforce(Ruleset{Grants: spec.Grants}); err != nil {
		fmt.Fprintln(os.Stderr, "aos sandbox:", err)
		os.Exit(126)
	}
	path, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "aos sandbox:", err)
		os.Exit(127)
	}
	err = syscall.Exec(path, spec.Argv, os.Environ())
	fmt.Fprintln(os.Stderr, "aos sandbox: exec:", err)
	os.Exit(126)
}

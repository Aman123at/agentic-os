package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// helperEnv marks the re-executed helper, which removes it before executing the
// confined program. The spec travels as argv chunks: a large Ruleset would exceed
// the kernel's 128 KiB limit for a single argument or environment string.
const helperEnv = "AOS_SANDBOX_HELPER"

const specChunk = 64 << 10

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
	var chunks []string
	for len(spec) > specChunk {
		chunks, spec = append(chunks, string(spec[:specChunk])), spec[specChunk:]
	}
	cmd := exec.Command(self, append(chunks, string(spec))...)
	cmd.Env = append(append([]string{}, env...), helperEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}},
	}
	return cmd, nil
}

// RunHelperIfRequested turns this process into the sandbox helper when it was
// started by Command. It returns only when the process is not a helper.
func RunHelperIfRequested() {
	if _, ok := os.LookupEnv(helperEnv); !ok {
		return
	}
	_ = os.Unsetenv(helperEnv)
	// Exec happens from this thread; keep it fixed while no_new_privs and Landlock apply.
	runtime.LockOSThread()
	var spec helperSpec
	if err := json.Unmarshal([]byte(strings.Join(os.Args[1:], "")), &spec); err != nil || len(spec.Argv) == 0 {
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

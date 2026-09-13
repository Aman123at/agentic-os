package hostcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/amantiwari/agentic-os/internal/sandbox"
)

// checkLandlock is prototype M0.1: the Agent Session ruleset.
func checkLandlock(ctx context.Context, m *machine, rec recorder) {
	abi := sandbox.ABI()
	rec.check("Landlock is available", abi >= 1, "ABI %d", abi)
	// no_new_privs must hold even without Landlock: it is what keeps sudo away.
	base, err := sandbox.Plan(sandbox.DefaultPolicy(m.opts.Home, m.opts.Shared), sandbox.RootFS())
	if err != nil {
		rec.add("plan ruleset", Fail, "%v", err)
		return
	}
	exit, out, confined := m.confined(ctx, base, `grep -q "^NoNewPrivs:[[:space:]]*1" /proc/self/status && ! sudo -n true 2>/dev/null`)
	rec.check("Agent processes have no_new_privs and cannot sudo", confined && exit == 0, "%s", describe(exit, out))
	if abi < 1 {
		rec.add("Agent Session confinement", Skip, "no Landlock: Agents fall back to policy checks only (ADR-0004)")
		return
	}

	home := m.opts.Home
	work := filepath.Join(home, m.tag)             // an ordinary top-level folder
	locked := filepath.Join(work, "locked")        // a nested Protected Path
	hidden := filepath.Join(work, "hidden")        // a Hidden path readable by uid, so only Landlock denies it
	topLocked := filepath.Join(home, m.tag+"-ssh") // a top-level Protected folder, like ~/.ssh
	topFile := filepath.Join(home, m.tag+"-rc")    // a top-level Protected file, like ~/.bashrc
	newTop := filepath.Join(home, m.tag+"-new")    // created by the Agent
	sharedProbe := filepath.Join(m.opts.Shared, m.tag)
	hasShared := isDir(m.opts.Shared)

	cleanup := func() {
		for _, p := range []string{work, topLocked, topFile, newTop, filepath.Join(home, m.tag+"-sym"),
			sharedProbe, filepath.Join(m.opts.Shared, m.tag+"-new")} {
			_ = os.RemoveAll(p)
		}
	}
	cleanup()
	defer cleanup()

	files := map[string]string{
		filepath.Join(work, "free", "file.txt"): "ordinary\n",
		filepath.Join(locked, "id_ed25519"):     "secret key\n",
		filepath.Join(hidden, "token"):          "token\n",
		filepath.Join(topLocked, "id_ed25519"):  "secret key\n",
		topFile:                                 "export PS1=x\n",
	}
	if hasShared {
		files[filepath.Join(sharedProbe, "report.pdf")] = "host file\n"
	}
	for p, content := range files {
		err := os.MkdirAll(filepath.Dir(p), 0o755)
		if err == nil {
			err = os.WriteFile(p, []byte(content), 0o644)
		}
		if err != nil {
			rec.add("create fixtures", Fail, "%v", err)
			return
		}
	}
	for _, root := range []string{work, topLocked, topFile, sharedProbe} {
		if err := chownTree(root, m.uid, m.gid); err != nil && !os.IsNotExist(err) {
			rec.add("create fixtures", Fail, "%v", err)
			return
		}
	}

	policy := sandbox.DefaultPolicy(home, m.opts.Shared)
	policy.Protected = append(policy.Protected, locked, topLocked, topFile)
	policy.Hidden = append(policy.Hidden, hidden)
	rs, err := sandbox.Plan(policy, sandbox.RootFS())
	if err != nil {
		rec.add("plan ruleset", Fail, "%v", err)
		return
	}

	q := shellQuote
	allowed := func(name, script string) {
		exit, out, confined := m.confined(ctx, rs, script)
		rec.check("Agent "+name, confined && exit == 0, "%s", describe(exit, out))
	}
	// denied passes only when the confined shell started and the operation then failed,
	// never because the sandbox itself failed to start.
	denied := func(name, script string) {
		exit, out, confined := m.confined(ctx, rs, script)
		rec.check("Agent cannot "+name, confined && exit != 0, "%s", describe(exit, out))
	}

	free := filepath.Join(work, "free")
	allowed("writes in a home folder", fmt.Sprintf("echo more >> %s && mkdir -p %s/sub && echo new > %s/sub/new.txt && rm -r %s/sub", q(free+"/file.txt"), q(free), q(free), q(free)))
	// A directory split around a Protected Path only grants create, so a new file in it
	// cannot be written until the Ruleset is re-planned (docs/m0-findings.md, F1).
	exit, out, confined = m.confined(ctx, rs, fmt.Sprintf("echo new > %s/new.txt", q(work)))
	rec.known("F1", "Agent writes a new file next to a Protected Path in one command", confined && exit == 0, "%s", describe(exit, out))
	allowed("writes in /tmp", fmt.Sprintf("echo x > /tmp/%s && rm /tmp/%s", m.tag, m.tag))
	allowed("creates a new top-level home folder", "mkdir "+q(newTop))

	stale, err := rs.Stale(sandbox.RootFS())
	rec.check("new top-level folder makes the ruleset stale", err == nil && stale, "stale=%v err=%v", stale, err)
	if rs2, err := sandbox.Plan(policy, sandbox.RootFS()); err == nil {
		exit, out, confined := m.confined(ctx, rs2, fmt.Sprintf("echo x > %s/f && cat %s/f", q(newTop), q(newTop)))
		rec.check("Agent writes in the new folder after re-planning", confined && exit == 0, "%s", describe(exit, out))
	}

	for _, target := range []string{filepath.Join(locked, "id_ed25519"), filepath.Join(topLocked, "id_ed25519"), topFile} {
		rel, _ := filepath.Rel(home, target)
		denied("overwrite Protected ~/"+rel, "echo pwned > "+q(target))
		denied("append to Protected ~/"+rel, "echo pwned >> "+q(target))
		denied("truncate Protected ~/"+rel, "truncate -s 0 "+q(target))
		denied("delete Protected ~/"+rel, "rm -f "+q(target))
		// Destinations are in a fully writable folder, so only the source's protection can refuse.
		denied("rename Protected ~/"+rel, "mv "+q(target)+" "+q(free+"/moved"))
		denied("hard-link Protected ~/"+rel+" into a writable folder", "ln "+q(target)+" "+q(free+"/link"))
		denied("delete Protected ~/"+rel+" from Python", "python3 -c "+q("import os,sys; os.unlink(sys.argv[1])")+" "+q(target))
	}
	denied("delete a Protected top-level folder", "rm -rf "+q(topLocked))
	denied("rename a Protected top-level folder", "mv "+q(topLocked)+" "+q(free+"/ssh"))
	denied("write a new file into a Protected folder", "echo pwned > "+q(topLocked+"/config"))
	denied("create a symlink in home", "ln -s /tmp "+q(filepath.Join(home, m.tag+"-sym")))
	denied("read a Hidden path", "cat "+q(hidden+"/token"))

	if secrets, _ := filepath.Glob("/run/secrets/*"); len(secrets) > 0 {
		// Output is discarded: the check must never print a secret.
		denied("read /run/secrets", "cat "+q(secrets[0])+" > /dev/null")
	} else {
		rec.add("Agent cannot read /run/secrets", Skip, "no secrets mounted")
	}

	if hasShared {
		denied("overwrite a file in the Shared Folder", "echo pwned > "+q(sharedProbe+"/report.pdf"))
		denied("delete a file in the Shared Folder", "rm -f "+q(sharedProbe+"/report.pdf"))
		denied("write a new file in the Shared Folder", "echo new > "+q(sharedProbe+"/new.txt"))
		// Create rights on home reach into the Shared Folder mounted beneath it (F2).
		exit, out, confined := m.confined(ctx, rs, "mkdir "+q(filepath.Join(m.opts.Shared, m.tag+"-new")))
		rec.known("F2", "Agent cannot create a folder in the Shared Folder", confined && exit != 0, "%s", describe(exit, out))
	} else {
		rec.add("Agent cannot modify the Shared Folder", Skip, "%s is not mounted", m.opts.Shared)
	}

	denied("use sudo", "sudo -n true")

	// A User Session (same uid) runs alongside; the Agent must not reach into it.
	sleeper := m.userCmd("exec sleep 60")
	if err := sleeper.Start(); err == nil {
		pid := strconv.Itoa(sleeper.Process.Pid)
		denied("read a User Session's environment", "cat /proc/"+pid+"/environ > /dev/null")
		if abi >= 6 {
			denied("signal a User Session", "kill -0 "+pid)
		} else {
			rec.add("Agent cannot signal a User Session", Skip, "needs Landlock ABI 6, have %d", abi)
		}
		_ = sleeper.Process.Kill()
		_ = sleeper.Wait()
	}

	exit, out = m.user(ctx, fmt.Sprintf("echo edit >> %s && echo edit >> %s && sudo -n true", q(topFile), q(filepath.Join(locked, "id_ed25519"))))
	rec.check("User Session edits Protected files and uses sudo", exit == 0, "%s", describe(exit, out))
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func chownTree(root string, uid, gid uint32) error {
	return filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, int(uid), int(gid))
	})
}

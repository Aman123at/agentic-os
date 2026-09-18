package hostcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Aman123at/agentic-os/internal/sandbox"
)

// checkLandlock is prototype M0.1, with the home layout decided after M0 (ADR-0004).
func checkLandlock(ctx context.Context, m *machine, rec recorder) {
	layout := m.opts.Layout
	abi := sandbox.ABI()
	rec.check("Landlock is available", abi >= 1, "ABI %d", abi)
	checkHomeLayout(layout, m.gid, rec)
	// no_new_privs must hold even without Landlock: it is what keeps sudo away.
	base, err := sandbox.Plan(layout.Policy(), sandbox.RootFS())
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

	home := layout.Home
	work := filepath.Join(home, m.tag)      // an ordinary top-level folder
	locked := filepath.Join(work, "locked") // a path the user locked inside home
	hidden := filepath.Join(work, "hidden") // a Hidden path readable by uid, so only Landlock denies it
	newTop := filepath.Join(home, m.tag+"-new")
	// Fixtures shaped like ~/.ssh and ~/.bashrc: targets in the Protected folder behind
	// root-owned symlinks in home. The real dotfiles are never written.
	protDir := filepath.Join(layout.Protected, m.tag+"-ssh")
	protFile := filepath.Join(layout.Protected, m.tag+"-rc")
	linkDir := filepath.Join(home, m.tag+"-ssh")
	linkFile := filepath.Join(home, m.tag+"-rc")
	sharedProbe := filepath.Join(layout.Shared, m.tag)
	hasShared := isDir(layout.Shared)

	cleanup := func() {
		for _, p := range []string{work, newTop, protDir, protFile, linkDir, linkFile, filepath.Join(home, m.tag+"-evil"),
			filepath.Join(home, ".ssh", m.tag), sharedProbe, filepath.Join(layout.Shared, m.tag+"-new")} {
			_ = os.RemoveAll(p)
		}
	}
	cleanup()
	defer cleanup()

	files := map[string]string{
		filepath.Join(work, "free", "file.txt"): "ordinary\n",
		filepath.Join(locked, "id_ed25519"):     "secret key\n",
		filepath.Join(hidden, "token"):          "token\n",
		filepath.Join(protDir, "id_ed25519"):    "secret key\n",
		protFile:                                "export PS1=x\n",
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
	for _, root := range []string{work, protDir, protFile, sharedProbe} {
		if err := chownTree(root, m.uid, m.gid); err != nil && !os.IsNotExist(err) {
			rec.add("create fixtures", Fail, "%v", err)
			return
		}
	}
	for link, target := range map[string]string{linkDir: protDir, linkFile: protFile} {
		if err := os.Symlink(target, link); err != nil {
			rec.add("create fixtures", Fail, "%v", err)
			return
		}
	}

	policy := layout.Policy()
	policy.Protected = append(policy.Protected, locked)
	policy.Hidden = append(policy.Hidden, hidden)
	rs, err := sandbox.Plan(policy, sandbox.RootFS())
	if err != nil {
		rec.add("plan ruleset", Fail, "%v", err)
		return
	}

	q := shellQuote
	allowedIn := func(rs sandbox.Ruleset, name, script string) {
		exit, out, confined := m.confined(ctx, rs, script)
		rec.check("Agent "+name, confined && exit == 0, "%s", describe(exit, out))
	}
	allowed := func(name, script string) { allowedIn(rs, name, script) }
	// denied passes only when the confined shell started and the operation then failed,
	// never because the sandbox itself failed to start.
	deniedIn := func(rs sandbox.Ruleset, name, script string) {
		exit, out, confined := m.confined(ctx, rs, script)
		rec.check("Agent cannot "+name, confined && exit != 0, "%s", describe(exit, out))
	}
	// Protection by layout is probed with the default ruleset, where home is fully
	// writable, so a split home can never be what refuses the operation.
	denied := func(name, script string) { deniedIn(base, name, script) }

	// The default ruleset, with nothing locked in home: home is not split (F1).
	free := filepath.Join(work, "free")
	allowedIn(base, "writes in a home folder", fmt.Sprintf("echo more >> %s && mkdir -p %s/sub && echo new > %s/sub/new.txt && rm -r %s/sub", q(free+"/file.txt"), q(free), q(free), q(free)))
	allowedIn(base, "creates a top-level home folder and works in it in one command (F1)",
		fmt.Sprintf("git init -q %[1]s && cd %[1]s && echo hi > f && git add f && git -c user.email=a@b -c user.name=a commit -qm x && mkdir -p a/b && echo x > a/b/c && rm -rf a", q(newTop)))
	allowedIn(base, "creates, moves and deletes a top-level home file", fmt.Sprintf("echo x > %[1]s && mv %[1]s %[2]s && rm %[2]s", q(newTop+".txt"), q(free+"/moved.txt")))
	allowedIn(base, "writes in /tmp", fmt.Sprintf("echo x > /tmp/%s && rm /tmp/%s", m.tag, m.tag))
	stale, err := base.Stale(sandbox.RootFS())
	rec.check("a new top-level home folder does not need re-planning", err == nil && !stale, "stale=%v err=%v", stale, err)

	// With a path locked inside home, every folder above it is split and only grants
	// create, so a new file there cannot be written until the Session is re-planned (ADR-0004).
	allowed("writes next to a locked path in an existing folder", "echo more >> "+q(free+"/file.txt"))
	exit, out, confined = m.confined(ctx, rs, fmt.Sprintf("echo new > %s/new.txt", q(work)))
	rec.known("F1", "Agent writes a new file next to a user-locked path in one command", confined && exit == 0, "%s", describe(exit, out))
	stale, err = rs.Stale(sandbox.RootFS())
	rec.check("a new entry next to a user-locked path needs re-planning", err == nil && stale, "stale=%v err=%v", stale, err)
	if rs2, err := sandbox.Plan(policy, sandbox.RootFS()); err == nil {
		exit, out, confined := m.confined(ctx, rs2, fmt.Sprintf("echo x > %s/new.txt && cat %s/new.txt", q(work), q(work)))
		rec.check("Agent writes the new file after re-planning", confined && exit == 0, "%s", describe(exit, out))
	}

	for _, target := range []string{filepath.Join(linkDir, "id_ed25519"), linkFile, filepath.Join(locked, "id_ed25519")} {
		rel, _ := filepath.Rel(home, target)
		denied := denied
		if strings.HasPrefix(target, locked) {
			denied = func(name, script string) { deniedIn(rs, name, script) }
		}
		denied("overwrite Protected ~/"+rel, "echo pwned > "+q(target))
		denied("append to Protected ~/"+rel, "echo pwned >> "+q(target))
		denied("truncate Protected ~/"+rel, "truncate -s 0 "+q(target))
		denied("delete Protected ~/"+rel, "rm -f "+q(target))
		// Destinations are in a fully writable folder, so only the source's protection can refuse.
		denied("rename Protected ~/"+rel, "mv "+q(target)+" "+q(free+"/moved"))
		denied("hard-link Protected ~/"+rel+" into a writable folder", "ln "+q(target)+" "+q(free+"/link"))
		denied("delete Protected ~/"+rel+" from Python", "python3 -c "+q("import os,sys; os.unlink(sys.argv[1])")+" "+q(target))
	}
	denied("delete a protected symlink", "rm -f "+q(linkFile))
	denied("rename a protected symlink", "mv "+q(linkDir)+" "+q(free+"/ssh"))
	denied("replace a protected symlink with ln -sf", "ln -sfn /tmp "+q(linkFile))
	denied("rename a file over a protected symlink", fmt.Sprintf("echo evil > %[1]s && mv -f %[1]s %[2]s", q(filepath.Join(home, m.tag+"-evil")), q(linkFile)))
	denied("delete a Protected folder through its symlink", "rm -rf "+q(linkDir+"/"))
	denied("write a new file into a Protected folder", "echo pwned > "+q(linkDir+"/config"))
	denied("write into the Protected folder directly", "echo pwned >> "+q(protFile))
	denied("create a file in the real ~/.ssh", "touch "+q(filepath.Join(home, ".ssh", m.tag)))
	deniedIn(rs, "read a Hidden path", "cat "+q(hidden+"/token"))

	if secrets, _ := filepath.Glob("/run/secrets/*"); len(secrets) > 0 {
		// Output is discarded: the check must never print a secret.
		denied("read /run/secrets", "cat "+q(secrets[0])+" > /dev/null")
	} else {
		rec.add("Agent cannot read /run/secrets", Skip, "no secrets mounted")
	}

	if hasShared {
		viaHome := filepath.Join(home, "Shared", m.tag)
		denied("overwrite a file in the Shared Folder", "echo pwned > "+q(viaHome+"/report.pdf"))
		denied("delete a file in the Shared Folder", "rm -f "+q(sharedProbe+"/report.pdf"))
		denied("write a new file in the Shared Folder", "echo new > "+q(viaHome+"/new.txt"))
		denied("create a folder in the Shared Folder (F2)", "mkdir "+q(filepath.Join(layout.Shared, m.tag+"-new")))
	} else {
		rec.add("Agent cannot modify the Shared Folder", Skip, "%s is not mounted", layout.Shared)
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

	exit, out = m.user(ctx, fmt.Sprintf("echo edit >> %s && echo edit >> %s && echo edit >> %s && sudo -n true",
		q(linkFile), q(filepath.Join(linkDir, "id_ed25519")), q(filepath.Join(locked, "id_ed25519"))))
	rec.check("User Session edits Protected files through their symlinks and uses sudo", exit == 0, "%s", describe(exit, out))
}

// checkHomeLayout verifies what aosd prepares at start (sandbox.PrepareHome).
func checkHomeLayout(l sandbox.Layout, gid uint32, rec recorder) {
	var problems []string
	if fi, err := os.Lstat(l.Home); err != nil {
		problems = append(problems, err.Error())
	} else if st := fi.Sys().(*syscall.Stat_t); st.Uid != 0 || st.Gid != gid || fi.Mode()&os.ModeSticky == 0 || fi.Mode().Perm() != 0o775 {
		problems = append(problems, fmt.Sprintf("%s is %d:%d %v, want 0:%d drwxrwxr-t", l.Home, st.Uid, st.Gid, fi.Mode(), gid))
	}
	links := map[string]string{filepath.Join(l.Home, "Shared"): l.Shared}
	for _, e := range sandbox.ProtectedEntries {
		links[filepath.Join(l.Home, e.Link)] = filepath.Join(l.Protected, e.Target)
	}
	for link, target := range links {
		fi, err := os.Lstat(link)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		dest, _ := os.Readlink(link)
		if st := fi.Sys().(*syscall.Stat_t); fi.Mode()&os.ModeSymlink == 0 || dest != target || st.Uid != 0 {
			problems = append(problems, fmt.Sprintf("%s is %v -> %q owned by %d, want a root-owned symlink to %s", link, fi.Mode(), dest, st.Uid, target))
		}
	}
	rec.check("home layout: sticky root-owned home, root-owned symlinks to Protected dotfiles and the Shared Folder",
		len(problems) == 0, "%d links checked; %v", len(links), problems)
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

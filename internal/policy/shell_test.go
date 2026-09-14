package policy

import (
	"slices"
	"testing"
)

// shellEnv is a Session in ~/proj where ~/proj/existing.txt and ~/proj/dir exist.
func shellEnv() ShellEnv {
	return ShellEnv{
		Cwd:  "/home/aos/proj",
		Home: home,
		Stat: func(path string) (exists, dir bool) {
			switch path {
			case "/home/aos/proj/existing.txt", "/home/aos/.bashrc":
				return true, false
			case "/home/aos/proj/dir", "/home/aos/proj", "/tmp":
				return true, true
			}
			return false, false
		},
	}
}

type shellCase struct {
	command string
	risky   bool
	effects []Effect
}

func checkShell(t *testing.T, cases []shellCase) {
	t.Helper()
	for _, tc := range cases {
		got := AnalyzeCommand(tc.command, shellEnv())
		if got.Risky != tc.risky {
			t.Errorf("%q: risky=%v, want %v (reasons %q)", tc.command, got.Risky, tc.risky, got.Reasons)
		}
		if tc.risky && len(got.Reasons) == 0 {
			t.Errorf("%q: risky without a reason", tc.command)
		}
		if !slices.Equal(got.Effects, tc.effects) {
			t.Errorf("%q: effects %v, want %v", tc.command, got.Effects, tc.effects)
		}
	}
}

func TestAnalyzeDeletions(t *testing.T) {
	checkShell(t, []shellCase{
		{"ls -la", false, nil},
		{"rm notes.txt", true, []Effect{{"/home/aos/proj/notes.txt", Delete}}},
		{"rm -rf ~/.ssh ../old", true, []Effect{{"/home/aos/.ssh", Delete}, {"/home/aos/old", Delete}}},
		{"rm -f -- -weird", true, []Effect{{"/home/aos/proj/-weird", Delete}}},
		{`rm "$HOME/a b"`, true, []Effect{{"/home/aos/a b", Delete}}},
		{"rm -r /tmp/build", false, []Effect{{"/tmp/build", Delete}}},
		{"rmdir empty && unlink link", true, []Effect{{"/home/aos/proj/empty", Delete}, {"/home/aos/proj/link", Delete}}},
		{"shred -u secret.key", true, []Effect{{"/home/aos/proj/secret.key", Delete}}},
		{"find . -name '*.log' -delete", true, []Effect{{"/home/aos/proj", Delete}}},
		{"rm $(cat list.txt)", true, nil},
		{"mv a.txt b.txt", false, []Effect{{"/home/aos/proj/a.txt", Delete}, {"/home/aos/proj/b.txt", Write}}},
		{"mv a.txt existing.txt", true, []Effect{{"/home/aos/proj/a.txt", Delete}, {"/home/aos/proj/existing.txt", Write}}},
		{"mv a.txt b.txt dir", false, []Effect{{"/home/aos/proj/a.txt", Delete}, {"/home/aos/proj/b.txt", Delete}, {"/home/aos/proj/dir", Write}}},
	})
}

func TestAnalyzeWrites(t *testing.T) {
	checkShell(t, []shellCase{
		{"echo hi > new.txt", false, []Effect{{"/home/aos/proj/new.txt", Write}}},
		{"echo hi > existing.txt", true, []Effect{{"/home/aos/proj/existing.txt", Write}}},
		{"echo hi >> existing.txt", false, []Effect{{"/home/aos/proj/existing.txt", Write}}},
		{"make 2> /dev/null &> /tmp/log 2>&1", false, []Effect{{"/tmp/log", Write}}},
		{"echo 'export X=1' >> ~/.bashrc", false, []Effect{{"/home/aos/.bashrc", Write}}},
		{"cp a.txt existing.txt", true, []Effect{{"/home/aos/proj/existing.txt", Write}}},
		{"cp -r src dir", false, []Effect{{"/home/aos/proj/dir", Write}}},
		{"tee out.txt existing.txt < in", true, []Effect{{"/home/aos/proj/out.txt", Write}, {"/home/aos/proj/existing.txt", Write}}},
		{"tee -a existing.txt", false, []Effect{{"/home/aos/proj/existing.txt", Write}}},
		{"dd if=/dev/zero of=disk.img bs=1M count=1", true, []Effect{{"/home/aos/proj/disk.img", Write}}},
		{"truncate -s 0 app.log", true, []Effect{{"/home/aos/proj/app.log", Write}}},
		{"sed -i 's/a/b/' config.ini notes.md", false, []Effect{{"/home/aos/proj/config.ini", Write}, {"/home/aos/proj/notes.md", Write}}},
		{"sed -e 's/a/b/' -i.bak config.ini", false, []Effect{{"/home/aos/proj/config.ini", Write}}},
		{"sed 's/a/b/' config.ini", false, nil},
		{"chmod 600 ~/.ssh/id_ed25519", false, []Effect{{"/home/aos/.ssh/id_ed25519", Write}}},
		{"touch a && mkdir -p b/c", false, []Effect{{"/home/aos/proj/a", Write}, {"/home/aos/proj/b/c", Write}}},
		{"ln -sf /etc/hosts hosts", false, []Effect{{"/home/aos/proj/hosts", Write}}},
		{"cd /tmp && echo x > f", false, []Effect{{"/tmp/f", Write}}},
	})
}

func TestAnalyzeGitUploadsAndSoftware(t *testing.T) {
	checkShell(t, []shellCase{
		{"git status && git add -A && git commit -m wip", false, nil},
		{"git reset --hard HEAD~1", true, []Effect{{"/home/aos/proj", Discard}}},
		{"git -C ../other clean -fdx", true, []Effect{{"/home/aos/other", Discard}}},
		{"git checkout -- src", true, []Effect{{"/home/aos/proj", Discard}}},
		{"git checkout main", false, nil},
		{"git restore .", true, []Effect{{"/home/aos/proj", Discard}}},
		{"git restore --staged file", false, nil},
		{"git stash drop", true, []Effect{{"/home/aos/proj", Discard}}},
		{"git push --force origin main", true, nil},
		{"git clone https://example.com/r.git", false, nil},
		{"curl -fsSLo app.tgz https://example.com/app.tgz", false, []Effect{{"/home/aos/proj/app.tgz", Write}}},
		{"curl -X POST -d @notes.txt https://example.com", true, nil},
		{"curl -T backup.tar ftp://example.com/", true, nil},
		{"curl -F file=@a.pdf https://example.com/upload", true, nil},
		{"wget -O page.html https://example.com", false, []Effect{{"/home/aos/proj/page.html", Write}}},
		{"wget --post-file=data.json https://example.com", true, nil},
		{"scp report.pdf host:/tmp/", true, nil},
		{"rsync -a ./site/ user@host:/var/www/", true, nil},
		{"rsync -a src/ /tmp/copy/", false, []Effect{{"/tmp/copy", Write}}},
		{"pipx uninstall httpie", true, nil},
		{"npm uninstall -g typescript", true, nil},
		{"npm install lodash", false, nil},
		{"apt-get remove -y nginx", true, nil},
		{"mkfs.ext4 /dev/loop0", true, nil},
	})
}

func TestAnalyzeWrappersAndNestedShells(t *testing.T) {
	checkShell(t, []shellCase{
		{"sudo rm -rf /var/log/app", true, []Effect{{"/var/log/app", Delete}}},
		{"env FOO=1 nice -n 5 timeout 30 rm old.txt", true, []Effect{{"/home/aos/proj/old.txt", Delete}}},
		{"nohup rm big.iso &", true, []Effect{{"/home/aos/proj/big.iso", Delete}}},
		{`bash -c "rm -rf ~/.config"`, true, []Effect{{"/home/aos/.config", Delete}}},
		{`sh -lc 'echo hi > existing.txt'`, true, []Effect{{"/home/aos/proj/existing.txt", Write}}},
		{"eval 'rm notes.txt'", true, []Effect{{"/home/aos/proj/notes.txt", Delete}}},
		{"ls *.tmp | xargs rm", true, nil},
		{"find . -type f -exec shred -u {} +", true, []Effect{{"/home/aos/proj", Delete}}},
		{"python3 -c 'import os; os.remove(\"x\")'", false, nil},
		{"for f in a b; do rm \"$f\"; done", true, nil},
		{"if true; then", false, nil},
	})
}

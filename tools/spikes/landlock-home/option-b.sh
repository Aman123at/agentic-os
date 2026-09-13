#!/bin/bash
# Option B: protected dotfiles live outside home behind root-owned symlinks; home
# is root:aos 1775 (sticky), the Shared Folder is mounted at /shared.
set -u
P=/home/.aos-protected
mkdir -p $P/ssh $P/gnupg $P/config
echo key > $P/ssh/id_ed25519
for f in .bashrc .profile .bash_logout; do mv /home/aos/$f $P/${f#.}; done
chown -R aos:aos $P/*; chmod 700 $P/ssh; chmod 755 $P
cd /home/aos
for n in ssh gnupg config bashrc profile bash_logout; do ln -s $P/$n .$n; chown -h root:root .$n; done
ln -s /shared Shared; chown -h root:root Shared
mkdir Documents && echo doc > Documents/a.txt && chown -R aos:aos Documents
chown root:aos /home/aos && chmod 1775 /home/aos
echo "protected_symlinks=$(cat /proc/sys/fs/protected_symlinks) protected_regular=$(cat /proc/sys/fs/protected_regular)"
ls -la /home/aos

run() { /t/sandbox-run -hidden /run/secrets -hidden /var/lib/aos -protected $P -protected /shared \
  -writable /home/aos -writable /tmp -writable /var/tmp -writable /dev -- bash -c "cd /home/aos; $1" >/tmp/out 2>&1; }
pass=0; fail=0
ok()   { if run "$2"; then echo "PASS  agent: $1"; pass=$((pass+1)); else echo "FAIL  agent: $1 :: $(head -c 200 /tmp/out)"; fail=$((fail+1)); fi; }
no()   { if run "$2"; then echo "FAIL  agent can: $1"; fail=$((fail+1)); else echo "PASS  agent cannot: $1 :: $(head -c 120 /tmp/out | tr '\n' ' ')"; pass=$((pass+1)); fi; }
info() { if run "$2"; then echo "INFO  agent CAN: $1"; else echo "INFO  agent cannot: $1 :: $(head -c 100 /tmp/out | tr '\n' ' ')"; fi; }

ok "git clone-like flow in a new top-level folder" 'git init -q ~/repo && cd ~/repo && echo hi > f && git add f && git -c user.email=a@b -c user.name=a commit -qm x'
ok "create, write, move, delete top-level file" 'echo data > ~/notes.txt && mv ~/notes.txt ~/Documents/ && rm ~/Documents/notes.txt'
ok "mkdir -p, write, rm -rf new tree" 'mkdir -p ~/proj/a/b && echo x > ~/proj/a/b/c && rm -rf ~/proj'
ok "python venv in new folder" 'python3 -m venv ~/venv && ~/venv/bin/python -c "print(1)" && rm -rf ~/venv'
ok "delete top-level folder" 'mkdir ~/junk && rm -rf ~/junk'
no "append to ~/.bashrc" 'echo pwned >> ~/.bashrc'
no "delete ~/.bashrc symlink" 'rm -f ~/.bashrc'
no "rename ~/.bashrc" 'mv ~/.bashrc ~/bashrc.bak'
no "replace ~/.bashrc symlink (ln -sf)" 'ln -sf /tmp/evil ~/.bashrc'
no "rename a file over ~/.bashrc" 'echo evil > ~/evil && mv -f ~/evil ~/.bashrc'
no "write new ~/.ssh/config" 'echo "ProxyCommand evil" > ~/.ssh/config'
no "delete ~/.ssh/id_ed25519" 'rm -f ~/.ssh/id_ed25519'
no "rm -rf ~/.ssh" 'rm -rf ~/.ssh'
no "write into /home/.aos-protected directly" 'echo x >> /home/.aos-protected/profile'
no "new file in Shared" 'echo x > ~/Shared/new.txt'
no "mkdir in Shared" 'mkdir ~/Shared/newdir'
no "delete file in Shared" 'rm -f ~/Shared/report.pdf'
no "delete ~/Shared symlink" 'rm -f ~/Shared'
no "sudo" 'sudo -n true'
info "chmod a Protected file (Landlock does not cover metadata)" 'chmod 600 /home/.aos-protected/ssh/id_ed25519'
info "create ~/.bash_profile (unprotected dotfile)" 'touch ~/.bash_profile && rm ~/.bash_profile'
info "create ~/.local/bin/sudo (PATH shadowing)" 'mkdir -p ~/.local/bin && echo "#!/bin/sh" > ~/.local/bin/sudo && rm -rf ~/.local'

u() { runuser -u aos -- bash -c "cd /home/aos; $1" >/tmp/uout 2>&1; }
if u 'echo "# user edit" >> ~/.bashrc && echo k >> ~/.ssh/id_ed25519 && git config --global user.name me && sudo -n true'; then echo "PASS  user: edits dotfiles through symlinks, git config --global, sudo"; pass=$((pass+1)); else echo "FAIL  user: $(cat /tmp/uout)"; fail=$((fail+1)); fi
if u "sed -i 's/user edit/x/' ~/.bashrc"; then echo "INFO  user: sed -i on ~/.bashrc works"; else echo "INFO  user: sed -i on ~/.bashrc fails (replaces symlink in sticky home) :: $(head -c 100 /tmp/uout)"; fi
if u "sed -i --follow-symlinks 's/user edit/x/' ~/.bashrc"; then echo "INFO  user: sed -i --follow-symlinks works"; fi
if u 'rm -f ~/.bashrc'; then echo "INFO  user CAN delete ~/.bashrc symlink"; else echo "INFO  user cannot delete ~/.bashrc symlink (sticky home)"; fi
echo "$pass passed, $fail failed"

package policy

import (
	"strings"
	"testing"
)

func TestSensitiveSystemFiles(t *testing.T) {
	for path, want := range map[string]string{
		"/etc/sudoers":                      "sudo",
		"/etc/sudoers.d/90-aos":             "sudo",
		"/etc/sudoers.d":                    "sudo",
		"/etc/passwd":                       "logging in",
		"/etc/shadow":                       "logging in",
		"/etc/fstab":                        "starting",
		"/etc/hosts":                        "network",
		"/etc/netplan/50-cloud-init.yaml":   "network",
		"/etc/ssh/sshd_config":              "SSH",
		"/etc/pam.d/common-auth":            "logging in",
		"/etc/systemd/system/nginx.service": "services",
		"/etc/systemd/system/aos.service":   "AOS",
		"/usr/local/bin/aosd":               "AOS",
		"/etc/apt/sources.list":             "software",
		"/etc/default/grub":                 "starting",
		"/etc/sysctl.d/99-net.conf":         "kernel",
		"/etc/cron.d/backup":                "schedule",
		"/boot/grub/grub.cfg":               "starting",
		"/usr/bin/ls":                       "every command",
		"/lib/systemd/system/ssh.service":   "every command",
		"/lib64/ld-linux-x86-64.so.2":       "every command",
		"/usr/lib/systemd/systemd":          "every command",
		"/root/.ssh/authorized_keys":        "let someone else in",
		"/home/ubuntu/.ssh/authorized_keys": "let someone else in",
		"/etc/./sudoers":                    "sudo",
	} {
		why, ok := Sensitive(path)
		if !ok || !strings.Contains(why, want) {
			t.Errorf("Sensitive(%q) = %q, %v; want a reason mentioning %q", path, why, ok, want)
		}
	}
	for _, path := range []string{
		"/etc/nginx/nginx.conf", "/etc/hostsfile", "/etc/sudoers.bak", "/home/aos/project/main.go",
		"/usr/local/bin/node", "/var/log/syslog", "/library/x", "/home/aos/.ssh/config", "etc/sudoers", "/",
	} {
		if why, ok := Sensitive(path); ok {
			t.Errorf("Sensitive(%q) = %q, want not sensitive", path, why)
		}
	}
}

func TestChangingASensitiveSystemFileIsRiskyInRootModeOnly(t *testing.T) {
	edit := Call{Tool: "edit_file", Effects: []Effect{{Path: "/etc/sudoers", Op: Write}}, Folder: "/etc"}
	root := func(a Autonomy, grants ...Grant) Context {
		return Context{Autonomy: a, Interactive: true, Landlock: true, Root: true, Grants: grants, Protected: protection()}
	}
	d := Decide(edit, root(ConfirmRisky))
	if d.Verdict != Ask || d.Grantable || !strings.Contains(strings.Join(d.Reasons, " "), "Sensitive System File /etc/sudoers") {
		t.Errorf("Root, confirm-risky: %+v, want an ungrantable Ask naming the file", d)
	}
	// auto still runs it, as for every Risky Action.
	if d := Decide(edit, root(Auto)); d.Verdict != Allow {
		t.Errorf("Root, auto: %+v, want Allow", d)
	}
	// A grant for edit_file in /etc does not cover it.
	if d := Decide(edit, root(ConfirmRisky, Grant{Tool: "edit_file", Folder: "/etc"})); d.Verdict != Ask {
		t.Errorf("Root, with a grant: %+v, want Ask", d)
	}
	// Other files in Root Mode are not Risky for it.
	other := Call{Tool: "edit_file", Effects: []Effect{{Path: "/etc/nginx/nginx.conf", Op: Write}}, Folder: "/etc/nginx"}
	if d := Decide(other, root(ConfirmRisky)); d.Verdict != Allow {
		t.Errorf("Root, nginx.conf: %+v, want Allow", d)
	}
	// Standard Mode is unchanged: /etc is a Protected Path there, and aos can't
	// write the file anyway; nothing adds a Sensitive reason.
	std := Context{Autonomy: ConfirmRisky, Interactive: true, Landlock: true, Protected: protection()}
	if d := Decide(edit, std); strings.Contains(strings.Join(d.Reasons, " "), "Sensitive") {
		t.Errorf("Standard: %+v, want no Sensitive reason", d)
	}
}

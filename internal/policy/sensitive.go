package policy

import (
	"path/filepath"
	"strings"
)

// A Sensitive System File is a file whose mistaken edit can lock people out
// of the Machine, stop it booting or break every command (PLAN.md M8.5). In
// Root Mode, where root may write all of them, saving one asks first — in
// Code, in FileService (which refuses it unconfirmed) and for an Agent (a
// Risky Action). One list serves all three. In Standard Mode aos can't write
// them anyway, so nothing asks.
type sensitiveRule struct {
	path string // an absolute path; a trailing "/" means everything under it
	why  string
}

const (
	whyAccounts = "A mistake here can stop every account, root included, from logging in."
	whySudo     = "A mistake here can lock everyone out of sudo."
	whyBoot     = "A mistake here can stop the Machine from starting."
	whyNetwork  = "A mistake here can cut the Machine off the network — and you off the Machine."
	whySSH      = "A mistake here can lock you out of SSH."
	whyLogin    = "A mistake here can stop anyone from logging in."
	whySystemd  = "A mistake here can stop services, AOS included, from starting."
	whyApt      = "A mistake here can break installing and updating software."
	whyKernel   = "A mistake here changes how the kernel behaves, and can stop the Machine from booting."
	whyCron     = "Commands here run as root on a schedule."
	whyPrograms = "These are the system's own programs and libraries; a mistake here can break every command."
	whyKeys     = "A mistake here can lock you out of SSH, or let someone else in."
	whyAOS      = "This is AOS itself; a mistake here can stop AOS from starting."
)

// sensitiveRules are checked in order, so a more specific rule comes before
// the folder that holds it.
var sensitiveRules = []sensitiveRule{
	{"/usr/local/bin/aosd", whyAOS},
	{"/usr/local/bin/aos", whyAOS},
	{"/etc/systemd/system/aos.service", whyAOS},
	{"/etc/passwd", whyAccounts},
	{"/etc/shadow", whyAccounts},
	{"/etc/group", whyAccounts},
	{"/etc/gshadow", whyAccounts},
	{"/etc/sudoers", whySudo},
	{"/etc/sudoers.d/", whySudo},
	{"/etc/fstab", whyBoot},
	{"/etc/crypttab", whyBoot},
	{"/etc/default/grub", whyBoot},
	{"/boot/", whyBoot},
	{"/etc/hosts", whyNetwork},
	{"/etc/hostname", whyNetwork},
	{"/etc/resolv.conf", whyNetwork},
	{"/etc/netplan/", whyNetwork},
	{"/etc/network/", whyNetwork},
	{"/etc/ssh/", whySSH},
	{"/etc/pam.d/", whyLogin},
	{"/etc/security/", whyLogin},
	{"/etc/systemd/", whySystemd},
	{"/etc/apt/", whyApt},
	{"/etc/sysctl.conf", whyKernel},
	{"/etc/sysctl.d/", whyKernel},
	{"/etc/modprobe.d/", whyKernel},
	{"/etc/crontab", whyCron},
	{"/etc/cron.d/", whyCron},
	{"/bin/", whyPrograms},
	{"/sbin/", whyPrograms},
	{"/usr/bin/", whyPrograms},
	{"/usr/sbin/", whyPrograms},
	{"/usr/lib/", whyPrograms},
}

// Sensitive reports whether path is a Sensitive System File, and if so what
// can go wrong when it is changed. path must be absolute.
func Sensitive(path string) (reason string, ok bool) {
	p := filepath.Clean(path)
	if !filepath.IsAbs(p) {
		return "", false
	}
	for _, r := range sensitiveRules {
		if dir, isDir := strings.CutSuffix(r.path, "/"); isDir {
			if p == dir || strings.HasPrefix(p, r.path) {
				return r.why, true
			}
		} else if p == r.path {
			return r.why, true
		}
	}
	// /lib, /lib32, /lib64 and /libx32.
	if top, _, _ := strings.Cut(p[1:], "/"); top == "lib" || top == "lib32" || top == "lib64" || top == "libx32" {
		return whyPrograms, true
	}
	// Every user's authorized_keys, root's included.
	if filepath.Base(p) == "authorized_keys" && filepath.Base(filepath.Dir(p)) == ".ssh" {
		return whyKeys, true
	}
	return "", false
}

package service

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Listener is a program listening on a TCP port.
type Listener struct {
	Port    int
	Address string
	PID     int
	// PGID is the process group, which is a Service's leader pid.
	PGID    int
	Process string
	Service string
}

// Internal reports whether this is plumbing rather than something the user
// opened: a loopback socket of aosd's own, or one of the container runtime's
// (Docker's embedded DNS resolver listens on 127.0.0.11 from outside the
// Machine's pid namespace, so it has no process to name). Listing either next to
// a real Service's port is noise (PLAN.md M4.8 item 8.14). A Service the user
// started on loopback has a pid of its own, so it stays listed.
func (l Listener) Internal() bool {
	ip := net.ParseIP(l.Address)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	return l.PID == 0 || l.PID == os.Getpid()
}

// socket is a listening socket from /proc/net/tcp or tcp6.
type socket struct {
	address string
	port    int
	inode   string
}

// parseProcNet reads the listening sockets (state 0A) of /proc/net/tcp or tcp6.
func parseProcNet(data string) []socket {
	var out []socket
	for _, line := range strings.Split(data, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		hostHex, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}
		addr, ok := decodeAddr(hostHex)
		if !ok {
			continue
		}
		out = append(out, socket{address: addr, port: int(port), inode: f[9]})
	}
	return out
}

// decodeAddr decodes an address from /proc/net/tcp: 32-bit words in host
// (little-endian) byte order.
func decodeAddr(h string) (string, bool) {
	b, err := hex.DecodeString(h)
	if err != nil || len(b)%4 != 0 {
		return "", false
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	switch len(b) {
	case net.IPv4len, net.IPv6len:
		return net.IP(b).String(), true
	}
	return "", false
}

// SocketsArg makes aosd print which of the processes it may inspect hold
// which sockets (`aosd __sockets`). Run as aos, it sees the Services that root
// can't: reading another user's /proc/<pid>/fd needs CAP_SYS_PTRACE, which
// Docker doesn't give the container.
const SocketsArg = "__sockets"

// SocketsMain is the body of `aosd __sockets`: socket inode → pid as JSON.
func SocketsMain() int {
	if err := json.NewEncoder(os.Stdout).Encode(socketOwners("/proc")); err != nil {
		return 1
	}
	return 0
}

// scanListeners finds the listening TCP sockets and the processes holding
// them; more adds owners found by `aosd __sockets`.
func scanListeners(procfs string, more map[string]int) ([]Listener, error) {
	var sockets []socket
	for _, name := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(procfs, "net", name))
		if err == nil {
			sockets = append(sockets, parseProcNet(string(data))...)
		}
	}
	if len(sockets) == 0 {
		return nil, nil
	}
	owners := socketOwners(procfs)
	for inode, pid := range more {
		if _, ok := owners[inode]; !ok {
			owners[inode] = pid
		}
	}
	byPort := map[int]Listener{}
	for _, s := range sockets {
		l := Listener{Port: s.port, Address: s.address}
		if pid, ok := owners[s.inode]; ok {
			l.PID, l.PGID, l.Process = pid, processGroup(procfs, pid), processName(procfs, pid)
		}
		// One entry per port (IPv4 and IPv6 sockets of one program): prefer a
		// known process, then the wildcard address.
		if prev, ok := byPort[s.port]; ok {
			if better := prev.PID == 0 && l.PID != 0 || !wildcard(prev.Address) && wildcard(l.Address); !better {
				continue
			}
		}
		byPort[s.port] = l
	}
	out := make([]Listener, 0, len(byPort))
	for _, l := range byPort {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out, nil
}

func wildcard(addr string) bool { return addr == "0.0.0.0" || addr == "::" }

// Reachable reports whether the listener is on the network past the Account: it
// binds a wildcard address (0.0.0.0 or ::), not just loopback. Natively there is
// no network namespace, so a wildcard bind is straight on the internet.
func (l Listener) Reachable() bool { return wildcard(l.Address) }

// socketOwners maps socket inodes to the pids whose file descriptors hold them.
func socketOwners(procfs string) map[string]int {
	owners := map[string]int{}
	dirs, _ := os.ReadDir(procfs)
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir(filepath.Join(procfs, d.Name(), "fd"))
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(procfs, d.Name(), "fd", fd.Name()))
			if inode, ok := strings.CutPrefix(target, "socket:["); err == nil && ok {
				if _, seen := owners[strings.TrimSuffix(inode, "]")]; !seen {
					owners[strings.TrimSuffix(inode, "]")] = pid
				}
			}
		}
	}
	return owners
}

func processName(procfs string, pid int) string {
	b, _ := os.ReadFile(filepath.Join(procfs, strconv.Itoa(pid), "comm"))
	return strings.TrimSpace(string(b))
}

// processGroup reads the process group from /proc/<pid>/stat, whose second
// field (the name in parentheses) may itself contain spaces.
func processGroup(procfs string, pid int) int {
	b, err := os.ReadFile(filepath.Join(procfs, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 3 {
		return 0
	}
	pgid, _ := strconv.Atoi(f[2]) // state, ppid, pgrp
	return pgid
}

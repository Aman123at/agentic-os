package service

import (
	"os"
	"path/filepath"
	"testing"
)

const netHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

// fakeProc writes a /proc with listening sockets and the processes holding them.
func fakeProc(t *testing.T) string {
	t.Helper()
	proc := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(proc, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("net/tcp", netHeader+
		"   0: 00000000:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5551 1 0000000000000000 100 0 0 10 0\n"+
		"   1: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5552 1 0000000000000000 100 0 0 10 0\n"+
		"   2: 0100007F:1E14 0100007F:A1B2 01 00000000:00000000 00:00000000 00000000  1000        0 5554 1 0000000000000000 100 0 0 10 0\n")
	write("net/tcp6", netHeader+
		"   0: 00000000000000000000000000000000:1F91 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5553 1 0000000000000000 100 0 0 10 0\n")
	process := func(pid, comm, stat string, sockets map[string]string) {
		write(pid+"/comm", comm+"\n")
		write(pid+"/stat", stat)
		if err := os.MkdirAll(filepath.Join(proc, pid, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		for fd, target := range sockets {
			if err := os.Symlink(target, filepath.Join(proc, pid, "fd", fd)); err != nil {
				t.Fatal(err)
			}
		}
	}
	process("200", "nginx", "200 (nginx) S 1 200 200 0 -1 4194560", map[string]string{"0": "/dev/null", "3": "socket:[5551]", "4": "socket:[5553]"})
	process("201", "nginx", "201 (nginx: worker) S 200 200 200 0 -1", map[string]string{"3": "socket:[5551]"})
	process("300", "node", "300 (node server) S 1 300 300 0 -1", map[string]string{"5": "socket:[5552]"})
	return proc
}

func TestOwnersRootCantSeeComeFromTheHelper(t *testing.T) {
	proc := fakeProc(t)
	// As in Docker: root can't read the fds of node, which another user runs.
	if err := os.RemoveAll(filepath.Join(proc, "300", "fd")); err != nil {
		t.Fatal(err)
	}
	without, _ := scanListeners(proc, nil)
	with, _ := scanListeners(proc, map[string]int{"5552": 300, "5551": 999})
	if without[0].Port != 3000 || without[0].PID != 0 {
		t.Errorf("without the helper: %+v", without[0])
	}
	if with[0].PID != 300 || with[0].Process != "node" || with[1].PID != 200 {
		t.Errorf("with the helper (which never overrides what root saw): %+v", with)
	}
}

func TestListenersComeFromProcfsWithTheirProcesses(t *testing.T) {
	got, err := scanListeners(fakeProc(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Listener{
		{Port: 3000, Address: "127.0.0.1", PID: 300, PGID: 300, Process: "node"},
		{Port: 8081, Address: "0.0.0.0", PID: 200, PGID: 200, Process: "nginx"},
	}
	if len(got) != len(want) {
		t.Fatalf("listeners %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("listener %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAddressesDecodeFromHostByteOrder(t *testing.T) {
	for hex, want := range map[string]string{
		"0100007F":                         "127.0.0.1",
		"00000000":                         "0.0.0.0",
		"00000000000000000000000001000000": "::1",
	} {
		if got, ok := decodeAddr(hex); !ok || got != want {
			t.Errorf("decodeAddr(%s) = %q, %v; want %q", hex, got, ok, want)
		}
	}
}

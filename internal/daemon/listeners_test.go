package daemon

import (
	"os"
	"testing"
)

// tempSock returns a short, unique socket path. It stays out of t.TempDir()
// because that path can exceed the ~104-byte limit a Unix socket allows.
func tempSock(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("/tmp", "aos-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	t.Cleanup(func() { os.Remove(name) })
	return name
}

// TestCliModeBindsOnlyTheSocket checks M6.4(d): cli Mode opens the control
// socket and no TCP port, while ui Mode opens both. The whole API is reachable
// over the socket, so the CLI keeps working; only the Desktop needs the port.
func TestCliModeBindsOnlyTheSocket(t *testing.T) {
	t.Run("cli", func(t *testing.T) {
		sock := tempSock(t)
		tcp, unix, err := bindListeners("cli", "127.0.0.1:0", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close()
		if tcp != nil {
			tcp.Close()
			t.Error("cli Mode opened a TCP listener")
		}
		if unix == nil {
			t.Error("cli Mode did not open the control socket")
		}
	})
	t.Run("ui", func(t *testing.T) {
		sock := tempSock(t)
		tcp, unix, err := bindListeners("ui", "127.0.0.1:0", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close()
		if tcp == nil {
			t.Fatal("ui Mode did not open a TCP listener")
		}
		tcp.Close()
	})
}

// TestBindListenersMakesTheSocketRootOnly checks the control socket is 0600, so
// only the user aosd runs as can reach the local root API (M6.2).
func TestBindListenersMakesTheSocketRootOnly(t *testing.T) {
	sock := tempSock(t)
	_, unix, err := bindListeners("cli", "127.0.0.1:0", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close()
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("socket mode %o, want 600", mode)
	}
}

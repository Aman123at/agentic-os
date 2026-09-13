package session

import (
	"bytes"
	"testing"
)

const nonce = "n0nce"

func startMarker(n string) string { return "\x1b]133;C;aos=" + n + "\x07" }
func endMarker(n, exit string) string {
	return "\x1b]133;D;aos=" + n + ";" + exit + "\x07"
}

func TestFrameReturnsOutputBetweenMarkersAndExitCode(t *testing.T) {
	stream := " __aos_run 1 n0nce\r\n" + startMarker(nonce) + "hello\r\n" + endMarker(nonce, "3") + "aos$ "

	f := NewFrame(nonce)
	out := f.Feed([]byte(stream))

	if string(out) != "hello\r\n" {
		t.Errorf("output = %q, want %q", out, "hello\r\n")
	}
	if !f.Done() || f.ExitCode() != 3 {
		t.Errorf("done=%v exit=%d, want done with exit 3", f.Done(), f.ExitCode())
	}
}

func TestFrameHandlesMarkersSplitAcrossChunks(t *testing.T) {
	stream := []byte("echo\r\n" + startMarker(nonce) + "a\x1bb\r\n" + endMarker(nonce, "127") + "aos$ ")

	for size := 1; size <= len(stream); size++ {
		f := NewFrame(nonce)
		var out []byte
		for i := 0; i < len(stream); i += size {
			out = append(out, f.Feed(stream[i:min(i+size, len(stream))])...)
		}
		if !bytes.Equal(out, []byte("a\x1bb\r\n")) || !f.Done() || f.ExitCode() != 127 {
			t.Fatalf("chunk size %d: output=%q done=%v exit=%d", size, out, f.Done(), f.ExitCode())
		}
	}
}

func TestFrameTreatsMarkersWithAnotherNonceAsOutput(t *testing.T) {
	fake := startMarker("other") + endMarker("other", "0") + "\x1b]133;D;aos=" + nonce + ";x\x07"
	stream := startMarker(nonce) + fake + endMarker(nonce, "0")

	f := NewFrame(nonce)
	out := f.Feed([]byte(stream))

	if string(out) != fake || !f.Done() || f.ExitCode() != 0 {
		t.Errorf("output=%q done=%v exit=%d, want the fake markers as output", out, f.Done(), f.ExitCode())
	}
}

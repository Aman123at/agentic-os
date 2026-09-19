package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// testPin builds an Installer and a zip served by an httptest server, wired so
// Install runs end to end without a network, a Linux kernel or a real browser.
// libs are the DT_NEEDED entries baked into the fake binary; have is what the
// faked ldconfig resolves.
func testPin(t *testing.T, libs, have []string) (*Installer, string) {
	t.Helper()
	zipBytes := makeZip(t, makeELF(libs))
	sum := sha256.Sum256(zipBytes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	t.Cleanup(srv.Close)

	haveSet := "\t"
	for _, h := range have {
		haveSet += h + " (libc6,x86-64) => /lib/" + h + "\n\t"
	}
	in := &Installer{
		Root:      filepath.Join(t.TempDir(), "aos-browser"),
		Arch:      "amd64",
		Pins:      map[string]Pin{"amd64": {Version: "1.2.3", Platform: "linux64", SHA256: hex.EncodeToString(sum[:])}},
		BaseURL:   srv.URL,
		FreeBytes: func(string) (uint64, error) { return 10 << 30, nil },
		Ldconfig:  func(context.Context) (string, error) { return haveSet, nil },
	}
	return in, hex.EncodeToString(sum[:])
}

func TestInstallSucceeds(t *testing.T) {
	in, _ := testPin(t, []string{"libnss3.so"}, []string{"libnss3.so"})
	if err := in.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	link := filepath.Join(in.Root, "chrome")
	if fi, err := os.Stat(link); err != nil || fi.IsDir() {
		t.Fatalf("chrome symlink missing: %v", err)
	}
	if _, err := os.Stat(in.Root + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("staging dir not cleaned up: %v", err)
	}
	if !in.Installed() {
		t.Error("Installed() = false after a successful install")
	}
	// A second install refuses rather than clobbering.
	if err := in.Install(context.Background()); err == nil {
		t.Error("re-install did not refuse")
	}
}

func TestInstallChecksumMismatch(t *testing.T) {
	in, want := testPin(t, []string{"libnss3.so"}, []string{"libnss3.so"})
	in.Pins["amd64"] = Pin{Version: "1.2.3", Platform: "linux64", SHA256: "deadbeef"}
	err := in.Install(context.Background())
	if err == nil {
		t.Fatal("Install accepted a checksum mismatch")
	}
	if want == "deadbeef" {
		t.Fatal("test setup: pins collided")
	}
	if _, statErr := os.Stat(in.Root); !os.IsNotExist(statErr) {
		t.Errorf("install dir left behind after mismatch: %v", statErr)
	}
	if _, statErr := os.Stat(in.Root + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("staging dir left behind after mismatch: %v", statErr)
	}
}

func TestInstallMissingLibrary(t *testing.T) {
	in, _ := testPin(t, []string{"libnss3.so", "libgbm.so.1"}, []string{"libnss3.so"})
	err := in.Install(context.Background())
	if err == nil {
		t.Fatal("Install did not refuse a missing library")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("libgbm.so.1")) {
		t.Errorf("error does not name the missing library: %v", err)
	}
	if _, statErr := os.Stat(in.Root); !os.IsNotExist(statErr) {
		t.Errorf("install dir left behind after a missing library: %v", statErr)
	}
}

func TestInstallRefusesLowSpace(t *testing.T) {
	in, _ := testPin(t, []string{"libnss3.so"}, []string{"libnss3.so"})
	in.FreeBytes = func(string) (uint64, error) { return 500 << 20, nil } // 500 MB
	// A download that would fail the test if reached: swap the server for one
	// that records being hit.
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	in.BaseURL = srv.URL
	if err := in.Install(context.Background()); err == nil {
		t.Fatal("Install did not refuse under 1 GB free")
	}
	if hit {
		t.Error("Install downloaded before checking free space")
	}
}

func TestInstallUnsupportedArch(t *testing.T) {
	in, _ := testPin(t, []string{"libnss3.so"}, []string{"libnss3.so"})
	in.Arch = "arm64"
	err := in.Install(context.Background())
	if err == nil {
		t.Fatal("Install did not refuse an unsupported arch")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("arm64")) {
		t.Errorf("error does not name the arch: %v", err)
	}
}

func TestRemove(t *testing.T) {
	in, _ := testPin(t, []string{"libnss3.so"}, []string{"libnss3.so"})
	if err := in.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := in.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if in.Installed() {
		t.Error("Installed() = true after Remove")
	}
	// Removing what is absent is not an error.
	if err := in.Remove(); err != nil {
		t.Errorf("Remove of absent browser: %v", err)
	}
}

func TestParseLdconfig(t *testing.T) {
	out := "\tlibnss3.so (libc6,x86-64) => /usr/lib/x86_64-linux-gnu/libnss3.so\n" +
		"\tlibc.so.6 (libc6,x86-64) => /usr/lib/x86_64-linux-gnu/libc.so.6\n"
	have := parseLdconfig(out)
	if !have["libnss3.so"] || !have["libc.so.6"] {
		t.Errorf("parseLdconfig missed a library: %v", have)
	}
	if have["libmissing.so"] {
		t.Error("parseLdconfig reported a library that is not present")
	}
}

// makeZip wraps a fake binary in the same layout the real download uses:
// chrome-headless-shell-linux64/chrome-headless-shell.
func makeZip(t *testing.T, bin []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A directory entry, then the executable.
	if _, err := zw.Create("chrome-headless-shell-linux64/"); err != nil {
		t.Fatal(err)
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "chrome-headless-shell-linux64/chrome-headless-shell", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(bin); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeELF builds a minimal 64-bit little-endian ELF whose dynamic section lists
// the given libraries as DT_NEEDED, enough for debug/elf's ImportedLibraries to
// read them. It avoids needing a compiler or a Linux host in the test.
func makeELF(libs []string) []byte {
	const (
		ehSize = 64 // ELF64 header
		shSize = 64 // ELF64 section header
	)
	// .dynstr: a leading NUL, then each library NUL-terminated.
	var dynstr bytes.Buffer
	dynstr.WriteByte(0)
	offsets := make([]uint64, len(libs))
	for i, lib := range libs {
		offsets[i] = uint64(dynstr.Len())
		dynstr.WriteString(lib)
		dynstr.WriteByte(0)
	}
	// .dynamic: a DT_NEEDED (tag 1) per library, then DT_NULL (tag 0). Each
	// entry is two 8-byte little-endian words (tag, value).
	var dyn bytes.Buffer
	for _, off := range offsets {
		writeU64(&dyn, 1)
		writeU64(&dyn, off)
	}
	writeU64(&dyn, 0)
	writeU64(&dyn, 0)
	// .shstrtab: section names.
	shstr := []byte("\x00.dynstr\x00.dynamic\x00.shstrtab\x00")
	nameDynstr := uint32(1)
	nameDynamic := uint32(9)
	nameShstrtab := uint32(18)

	// Lay out section data after the ELF header.
	off := uint64(ehSize)
	dynstrOff := off
	off += uint64(dynstr.Len())
	dynOff := off
	off += uint64(dyn.Len())
	shstrOff := off
	off += uint64(len(shstr))
	shOff := off

	var b bytes.Buffer
	// ELF header.
	b.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}) // e_ident
	writeU16(&b, 3)                                                          // e_type = ET_DYN
	writeU16(&b, 62)                                                         // e_machine = EM_X86_64
	writeU32(&b, 1)                                                          // e_version
	writeU64(&b, 0)                                                          // e_entry
	writeU64(&b, 0)                                                          // e_phoff
	writeU64(&b, shOff)                                                      // e_shoff
	writeU32(&b, 0)                                                          // e_flags
	writeU16(&b, ehSize)                                                     // e_ehsize
	writeU16(&b, 0)                                                          // e_phentsize
	writeU16(&b, 0)                                                          // e_phnum
	writeU16(&b, shSize)                                                     // e_shentsize
	writeU16(&b, 4)                                                          // e_shnum
	writeU16(&b, 3)                                                          // e_shstrndx

	b.Write(dynstr.Bytes())
	b.Write(dyn.Bytes())
	b.Write(shstr)

	// Section headers: NULL, .dynstr, .dynamic (Link=1), .shstrtab.
	writeSH(&b, 0, 0, 0, 0, 0, 0)                                     // SHT_NULL
	writeSH(&b, nameDynstr, 3, dynstrOff, uint64(dynstr.Len()), 0, 0) // SHT_STRTAB
	writeSH(&b, nameDynamic, 6, dynOff, uint64(dyn.Len()), 1, 16)     // SHT_DYNAMIC, Link=.dynstr
	writeSH(&b, nameShstrtab, 3, shstrOff, uint64(len(shstr)), 0, 0)  // SHT_STRTAB
	return b.Bytes()
}

func writeSH(b *bytes.Buffer, name, typ uint32, offset, size uint64, link uint32, entsize uint64) {
	writeU32(b, name)    // sh_name
	writeU32(b, typ)     // sh_type
	writeU64(b, 0)       // sh_flags
	writeU64(b, 0)       // sh_addr
	writeU64(b, offset)  // sh_offset
	writeU64(b, size)    // sh_size
	writeU32(b, link)    // sh_link
	writeU32(b, 0)       // sh_info
	writeU64(b, 0)       // sh_addralign
	writeU64(b, entsize) // sh_entsize
}

func writeU16(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.LittleEndian, v) }
func writeU32(b *bytes.Buffer, v uint32) { _ = binary.Write(b, binary.LittleEndian, v) }
func writeU64(b *bytes.Buffer, v uint64) { _ = binary.Write(b, binary.LittleEndian, v) }

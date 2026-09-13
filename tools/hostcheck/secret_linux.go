package hostcheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const secretPath = "/run/secrets/openai_api_key"

// checkSecret is prototype M0.4: the OpenAI key arrives only as a Compose secret
// file. It compares bytes and reports only yes/no: the key is never printed.
func checkSecret(rec recorder) {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "OPENAI_API_KEY" {
			rec.add("OPENAI_API_KEY is not an environment variable", Fail, "found in this process's environment")
			return
		}
	}
	rec.add("OPENAI_API_KEY is not an environment variable", Pass, "")

	fi, err := os.Stat(secretPath)
	if err != nil {
		rec.add("secret file is mounted", Skip, "%s: %v", secretPath, err)
		return
	}
	st := fi.Sys().(*syscall.Stat_t)
	detail := "owner " + strconv.FormatUint(uint64(st.Uid), 10) + ":" + strconv.FormatUint(uint64(st.Gid), 10) + ", mode " + fi.Mode().Perm().String()
	if fi.Size() == 0 {
		rec.add("secret file is mounted", Skip, "%s; empty: OPENAI_API_KEY was not set on the Host", detail)
		return
	}
	rec.add("secret file is mounted", Pass, "%s, %d bytes", detail, fi.Size())
	rec.check("secret file is owned by root and not writable by others", st.Uid == 0 && fi.Mode().Perm()&0o022 == 0, "%s", detail)

	key, err := os.ReadFile(secretPath)
	if err != nil {
		rec.add("key is absent from process environments", Fail, "%v", err)
		return
	}
	key = bytes.TrimSpace(key)
	defer clear(key)
	if len(key) < 8 {
		rec.add("key is absent from process environments", Skip, "secret file holds no usable key")
		return
	}

	leaks := 0
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		for _, f := range []string{"environ", "cmdline"} {
			if b, err := os.ReadFile(filepath.Join(p, f)); err == nil && bytes.Contains(b, key) {
				leaks++
			}
		}
	}
	rec.check("key is absent from every /proc/*/environ and cmdline", leaks == 0, "%d processes scanned, %d leaks", len(procs), leaks)
}

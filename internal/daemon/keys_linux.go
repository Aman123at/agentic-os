package daemon

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

// keyStore keeps the OpenAI API key (PLAN.md §7.7) in a root-only file that
// only aosd's llm package reads. At startup the Compose secret is copied there,
// unless a key was saved from System Settings: that one wins, across restarts,
// until the user goes back to the key from .env.
type keyStore struct {
	secret string // the Compose secret
	file   string // the key aosd uses
}

var keys = keyStore{secret: SecretKey, file: keyFile}

// marker exists while the key file holds a key saved from System Settings.
func (k keyStore) marker() string { return k.file + ".from-settings" }

func (k keyStore) fromSettings() bool {
	_, err := os.Stat(k.marker())
	return err == nil
}

// copy puts the Compose secret in place, unless a key saved from System
// Settings is there. An empty or missing secret leaves the file as it is.
func (k keyStore) copy() error {
	if k.fromSettings() {
		return nil
	}
	key, err := os.ReadFile(k.secret)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if s := strings.TrimSpace(string(key)); s != "" {
		return writeKey(k.file, s)
	}
	return nil
}

// read returns the key in use, or "".
func (k keyStore) read() string {
	b, err := os.ReadFile(k.file)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Set saves a key from System Settings and returns its hint. The key is written
// before the marker, so a failure part-way leaves the .env key to come back at
// the next start rather than a marker over the wrong key.
func (k keyStore) Set(key string) (string, error) {
	key = strings.TrimSpace(key)
	if err := validKey(key); err != nil {
		return "", err
	}
	if err := writeKey(k.file, key); err != nil {
		return "", err
	}
	if err := os.WriteFile(k.marker(), nil, 0o400); err != nil {
		return "", err
	}
	return keyHint(key), nil
}

// Clear forgets the key saved from System Settings and puts the Compose
// secret back; it returns the hint of the key now in use, or "".
func (k keyStore) Clear() (string, error) {
	for _, p := range []string{k.marker(), k.file} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	if err := k.copy(); err != nil {
		return "", err
	}
	return keyHint(k.read()), nil
}

// status describes the key for SystemService.Info: present | empty | missing,
// where it came from (settings | env, when present) and its hint.
func (k keyStore) status() (state, source, hint string) {
	if key := k.read(); key != "" {
		source = "env"
		if k.fromSettings() {
			source = "settings"
		}
		return "present", source, keyHint(key)
	}
	if _, err := os.Stat(k.secret); err == nil {
		return "empty", "", ""
	}
	return "missing", "", ""
}

func writeKey(path, key string) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(key), 0o400); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validKey(key string) error {
	if len(key) < 20 || len(key) > 400 || strings.ContainsFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return errors.New("that is not an API key: it is one line of letters, digits and dashes, such as sk-…")
	}
	return nil
}

// keyHint shows a key the way System Settings does: sk-…abcd.
func keyHint(key string) string {
	if len(key) < 8 {
		return ""
	}
	return key[:3] + "…" + key[len(key)-4:]
}

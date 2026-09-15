package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// The API key (PLAN.md §7.7): the Compose secret is copied at startup, a key
// saved from System Settings wins over it across restarts until cleared, and
// only a hint of either ever leaves this file.

const (
	envKey      = "sk-env-000000000000000000001234"
	settingsKey = "sk-settings-0000000000000000abcd"
)

func testKeys(t *testing.T, secret *string) keyStore {
	t.Helper()
	dir := t.TempDir()
	k := keyStore{secret: filepath.Join(dir, "secret"), file: filepath.Join(dir, "keys", "openai")}
	if err := os.MkdirAll(filepath.Dir(k.file), 0o700); err != nil {
		t.Fatal(err)
	}
	if secret != nil {
		// World-readable, as Compose delivers it.
		if err := os.WriteFile(k.secret, []byte(*secret+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	return k
}

// secretIsRootOnly fails unless copy made the Compose secret unreadable to others.
func secretIsRootOnly(t *testing.T, k keyStore) {
	t.Helper()
	if fi, err := os.Stat(k.secret); err != nil || fi.Mode().Perm() != 0o400 {
		t.Errorf("the Compose secret's mode is %v (%v), want 0400", fi.Mode().Perm(), err)
	}
}

func TestTheComposeSecretIsCopiedToARootOnlyFile(t *testing.T) {
	secret := envKey
	k := testKeys(t, &secret)
	if err := k.copy(); err != nil {
		t.Fatal(err)
	}
	if got := k.read(); got != envKey {
		t.Fatalf("read %q, want the secret", got)
	}
	if fi, err := os.Stat(k.file); err != nil || fi.Mode().Perm() != 0o400 {
		t.Errorf("key file mode: %v %v, want 0400", fi.Mode().Perm(), err)
	}
	secretIsRootOnly(t, k)
	if state, source, hint := k.status(); state != "present" || source != "env" || hint != "sk-…1234" {
		t.Errorf("status %s %s %s", state, source, hint)
	}
}

func TestAKeySavedFromSettingsWinsAcrossRestartsUntilCleared(t *testing.T) {
	secret := envKey
	k := testKeys(t, &secret)
	if err := k.copy(); err != nil {
		t.Fatal(err)
	}
	if hint, err := k.Set(settingsKey); err != nil || hint != "sk-…abcd" {
		t.Fatalf("set: %q, %v", hint, err)
	}
	if err := k.copy(); err != nil { // aosd restarts
		t.Fatal(err)
	}
	if got := k.read(); got != settingsKey {
		t.Fatalf("after a restart the key is %q, want the one saved from Settings", got)
	}
	secretIsRootOnly(t, k) // locked even when the Settings key wins
	if _, source, _ := k.status(); source != "settings" {
		t.Errorf("source %q, want settings", source)
	}

	if hint, err := k.Clear(); err != nil || hint != "sk-…1234" {
		t.Fatalf("clear: %q, %v", hint, err)
	}
	if got := k.read(); got != envKey {
		t.Errorf("after clearing the key is %q, want the Compose secret", got)
	}
	if _, source, _ := k.status(); source != "env" {
		t.Errorf("source %q after clearing, want env", source)
	}
}

func TestAnInvalidKeyIsRefusedAndTheOldOneKept(t *testing.T) {
	secret := envKey
	k := testKeys(t, &secret)
	if err := k.copy(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "sk-short", "sk-has a space-000000000000", "sk-two\nlines-0000000000000"} {
		if _, err := k.Set(bad); err == nil {
			t.Errorf("set(%q) was accepted", bad)
		}
	}
	if got := k.read(); got != envKey {
		t.Errorf("a refused key replaced the old one: %q", got)
	}
}

func TestWithoutAKeyStatusSaysWhy(t *testing.T) {
	if state, _, _ := testKeys(t, nil).status(); state != "missing" {
		t.Errorf("no secret at all: %s, want missing", state)
	}
	empty := ""
	k := testKeys(t, &empty)
	if err := k.copy(); err != nil {
		t.Fatal(err)
	}
	if state, _, hint := k.status(); state != "empty" || hint != "" {
		t.Errorf("an empty secret: %s %q, want empty", state, hint)
	}
}

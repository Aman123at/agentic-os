package cli

import (
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/auth"
)

// TestGeneratedPasswordsAreStrongAndUnique checks the password `aos mode ui`
// prints: it clears the model's minimum length with room to spare, uses only the
// unambiguous alphabet, and does not repeat (crypto/rand, not a fixed seed).
func TestGeneratedPasswordsAreStrongAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p, err := generatePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) < auth.MinPasswordLength {
			t.Fatalf("password %q is shorter than the %d-character minimum", p, auth.MinPasswordLength)
		}
		for _, r := range p {
			if !strings.ContainsRune(passwordAlphabet, r) {
				t.Fatalf("password %q contains %q, outside the alphabet", p, r)
			}
		}
		if seen[p] {
			t.Fatalf("generatePassword repeated a value: %q", p)
		}
		seen[p] = true
	}
}

// TestModeUIGuardsOnEmptyUser checks the local guard: `aos mode ui --user ""`
// fails before it reaches aosd, so the account is never created without a name
// to sign in with. The account-already-exists guard lives in aosd
// (CreateInitialUser), exercised by the auth tests.
func TestModeUIGuardsOnEmptyUser(t *testing.T) {
	cmd := modeUICmd()
	cmd.SetArgs([]string{"--user", ""})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil {
		t.Fatal("mode ui accepted an empty username")
	}
}

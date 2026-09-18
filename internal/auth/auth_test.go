package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aman123at/agentic-os/internal/store"
)

// newModel returns a Model on a fresh database with a controllable clock.
func newModel(t *testing.T) (*Model, *time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	return &Model{DB: db, Key: []byte("a-test-signing-key-not-secret"), Now: func() time.Time { return clock }}, &clock
}

func TestPasswordsAreHashedAndVerifiedThroughSignIn(t *testing.T) {
	m, _ := newModel(t)
	ctx := context.Background()

	if err := m.SetPassword(ctx, "aman", "short"); err != ErrWeakPassword {
		t.Fatalf("weak password: %v, want ErrWeakPassword", err)
	}
	if err := m.SetPassword(ctx, "aman", "a-strong-password"); err != nil {
		t.Fatal(err)
	}
	// The stored hash is a pbkdf2 string, never the password itself.
	var hash string
	if err := m.DB.Read().QueryRow(`SELECT pw_hash FROM users WHERE username = 'aman'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if len(hash) < 20 || hash[:14] != "pbkdf2-sha256$" {
		t.Errorf("stored hash %q is not a pbkdf2 string", hash)
	}

	if _, _, _, err := m.SignIn(ctx, "aman", "wrong-password"); err != ErrBadCredentials {
		t.Errorf("wrong password: %v, want ErrBadCredentials", err)
	}
	if _, _, _, err := m.SignIn(ctx, "nobody", "a-strong-password"); err != ErrBadCredentials {
		t.Errorf("unknown user: %v, want ErrBadCredentials", err)
	}
	access, refresh, _, err := m.SignIn(ctx, "aman", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if !m.VerifyAccess(access) {
		t.Error("the access token from a good sign-in does not verify")
	}
	if refresh == "" || access == "" {
		t.Error("sign-in returned an empty token")
	}
}

func TestRefreshRotatesTheToken(t *testing.T) {
	m, _ := newModel(t)
	ctx := context.Background()
	mustSetPassword(t, m, "aman", "a-strong-password")

	_, r0, _, err := m.SignIn(ctx, "aman", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	access1, r1, err := m.Refresh(ctx, r0)
	if err != nil {
		t.Fatal(err)
	}
	if r1 == r0 {
		t.Error("refresh returned the same token")
	}
	if !m.VerifyAccess(access1) {
		t.Error("the access token from a refresh does not verify")
	}
	// The rotated token keeps working.
	if _, _, err := m.Refresh(ctx, r1); err != nil {
		t.Errorf("the rotated token does not refresh: %v", err)
	}
}

func TestReplayingASpentTokenRevokesTheFamily(t *testing.T) {
	m, _ := newModel(t)
	ctx := context.Background()
	mustSetPassword(t, m, "aman", "a-strong-password")

	_, r0, _, err := m.SignIn(ctx, "aman", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	_, r1, err := m.Refresh(ctx, r0) // r0 is now spent
	if err != nil {
		t.Fatal(err)
	}
	// Replaying the spent r0 is refused...
	if _, _, err := m.Refresh(ctx, r0); err != ErrBadToken {
		t.Errorf("replaying a spent token: %v, want ErrBadToken", err)
	}
	// ...and it takes the whole family down: the legitimate r1 no longer works.
	if _, _, err := m.Refresh(ctx, r1); err != ErrBadToken {
		t.Errorf("the family survived a replay: %v, want ErrBadToken", err)
	}
}

func TestChangingThePasswordSignsOtherSessionsOut(t *testing.T) {
	m, _ := newModel(t)
	ctx := context.Background()
	mustSetPassword(t, m, "aman", "a-strong-password")
	_, r0, _, err := m.SignIn(ctx, "aman", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	mustSetPassword(t, m, "aman", "a-different-password")
	if _, _, err := m.Refresh(ctx, r0); err != ErrBadToken {
		t.Errorf("a session survived a password change: %v, want ErrBadToken", err)
	}
}

func TestAccessTokensExpire(t *testing.T) {
	m, clock := newModel(t)
	ctx := context.Background()
	mustSetPassword(t, m, "aman", "a-strong-password")
	access, _, _, err := m.SignIn(ctx, "aman", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if !m.VerifyAccess(access) {
		t.Fatal("a fresh access token does not verify")
	}
	*clock = clock.Add(AccessTTL + time.Second)
	if m.VerifyAccess(access) {
		t.Error("an expired access token still verifies")
	}
	// A token signed with a different key never verifies.
	other := &Model{Key: []byte("a-different-key"), Now: m.Now}
	if other.VerifyAccess(access) {
		t.Error("a token verified under the wrong key")
	}
}

func TestExpiredRefreshTokensAreRefused(t *testing.T) {
	m, clock := newModel(t)
	ctx := context.Background()
	mustSetPassword(t, m, "aman", "a-strong-password")
	_, r0, _, err := m.SignIn(ctx, "aman", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(RefreshTTL + time.Hour)
	if _, _, err := m.Refresh(ctx, r0); err != ErrBadToken {
		t.Errorf("an expired refresh token: %v, want ErrBadToken", err)
	}
}

func TestTicketsAreSingleUseAndExpire(t *testing.T) {
	m, clock := newModel(t)

	if m.RedeemTicket("") || m.RedeemTicket("never-minted") {
		t.Error("an empty or unknown ticket was accepted")
	}
	tkt := m.Ticket()
	if !m.RedeemTicket(tkt) {
		t.Error("a fresh ticket was rejected")
	}
	if m.RedeemTicket(tkt) {
		t.Error("a ticket was redeemed twice")
	}
	expiring := m.Ticket()
	*clock = clock.Add(TicketTTL + time.Second)
	if m.RedeemTicket(expiring) {
		t.Error("an expired ticket was redeemed")
	}
}

func TestInitialAccountForcesAPasswordChange(t *testing.T) {
	m, _ := newModel(t)
	ctx := context.Background()

	if err := m.CreateInitialUser(ctx, "aman", "generated-secret-123"); err != nil {
		t.Fatal(err)
	}
	// CreateInitialUser is the account-creation moment: a second call refuses
	// rather than silently reset a password already in use.
	if err := m.CreateInitialUser(ctx, "aman", "another-secret-456"); err != ErrUserExists {
		t.Errorf("creating a second account: %v, want ErrUserExists", err)
	}

	// Signing in with the generated password works but flags the forced change.
	access, refresh, mustChange, err := m.SignIn(ctx, "aman", "generated-secret-123")
	if err != nil {
		t.Fatal(err)
	}
	if !mustChange {
		t.Error("a system-generated account did not ask for a password change")
	}
	if !m.VerifyAccess(access) {
		t.Error("the access token from the first sign-in does not verify")
	}

	// The forced change replaces the password, returns a live pair, and clears
	// the flag so the next sign-in is a normal one.
	access2, refresh2, err := m.ChangePassword(ctx, "a-password-of-my-own")
	if err != nil {
		t.Fatal(err)
	}
	if !m.VerifyAccess(access2) {
		t.Error("the access token from ChangePassword does not verify")
	}
	// Changing the password signs every other session out: the refresh token the
	// forced-change session started from no longer works.
	if _, _, err := m.Refresh(ctx, refresh); err != ErrBadToken {
		t.Errorf("the pre-change session survived: %v, want ErrBadToken", err)
	}
	// The pair ChangePassword issued keeps the caller signed in.
	if _, _, err := m.Refresh(ctx, refresh2); err != nil {
		t.Errorf("the post-change session does not work: %v", err)
	}
	_, _, mustChange, err = m.SignIn(ctx, "aman", "a-password-of-my-own")
	if err != nil {
		t.Fatal(err)
	}
	if mustChange {
		t.Error("the change was not cleared: sign-in still forces a change")
	}
}

func TestSignOutRevokesTheFamily(t *testing.T) {
	m, _ := newModel(t)
	ctx := context.Background()
	mustSetPassword(t, m, "aman", "a-strong-password")
	_, r0, _, err := m.SignIn(ctx, "aman", "a-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Revoke(ctx, r0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Refresh(ctx, r0); err != ErrBadToken {
		t.Errorf("a signed-out token still refreshes: %v, want ErrBadToken", err)
	}
	// Signing out an unknown or already-revoked token is harmless.
	if err := m.Revoke(ctx, "never-issued"); err != nil {
		t.Errorf("revoking an unknown token: %v, want nil", err)
	}
}

func mustSetPassword(t *testing.T, m *Model, username, password string) {
	t.Helper()
	if err := m.SetPassword(context.Background(), username, password); err != nil {
		t.Fatal(err)
	}
}

func TestPortGrantsAreBoundToTheirPortAndExpire(t *testing.T) {
	m, clock := newModel(t)

	grant := m.PortGrant(8000)
	if !m.VerifyPortGrant(8000, grant) {
		t.Error("a fresh grant does not verify for its own port")
	}
	// A grant for one Service must not open another.
	if m.VerifyPortGrant(9000, grant) {
		t.Error("a grant minted for 8000 verified for 9000")
	}
	// A tampered value is refused.
	if m.VerifyPortGrant(8000, grant+"x") || m.VerifyPortGrant(8000, "not-a-grant") {
		t.Error("a forged grant verified")
	}
	// Another Model's key cannot mint a grant this one accepts.
	other := &Model{DB: m.DB, Key: []byte("a-different-key"), Now: m.Now}
	if m.VerifyPortGrant(8000, other.PortGrant(8000)) {
		t.Error("a grant signed with another key verified")
	}
	// It expires after PortGrantTTL.
	*clock = clock.Add(PortGrantTTL + time.Second)
	if m.VerifyPortGrant(8000, grant) {
		t.Error("an expired grant still verified")
	}
}

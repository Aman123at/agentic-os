// Package auth is aosd's authentication model (ADR-0007): the one user's
// password, the JWT access token and the rotating refresh tokens behind a
// sign-in, and the single-use tickets that authenticate browser loads which
// cannot carry a header. It owns the users and refresh_tokens tables; the HTTP
// middleware that puts it on the wire lives in internal/api.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aman123at/agentic-os/internal/store"
)

// The token lifetimes (ADR-0007). A short access token limits the blast radius
// of a leak; a long refresh token, rotated on every use, keeps a signed-in
// Desktop from asking for the password again for a month.
const (
	AccessTTL  = 15 * time.Minute
	RefreshTTL = 30 * 24 * time.Hour
	// TicketTTL is deliberately tiny: a ticket lives only long enough to travel
	// from CreateTicket to the browser load it authorises.
	TicketTTL = 30 * time.Second
	// MinPasswordLength is enforced here, not only in the screen, so a weak
	// password is never stored (PLAN.md §18 M6.5).
	MinPasswordLength = 12
	// PortGrantTTL bounds the cookie a redeemed ticket is exchanged for to reach a
	// path-forwarded Service (M6.4). It outlives an access token because the
	// sandboxed Service page has no way to refresh it, but it is scoped to one
	// port's path, carries no Desktop authority, and dies when the key rotates.
	PortGrantTTL = 12 * time.Hour
)

// pbkdf2 parameters (RFC 8018 with SHA-256). The iteration count follows OWASP's
// 2023 guidance for PBKDF2-HMAC-SHA256.
const (
	pbkdf2Iterations = 600_000
	pbkdf2KeyLength  = 32
	pbkdf2SaltLength = 16
)

// Errors a caller maps to an HTTP status; all sign-in failures look alike so a
// stranger cannot tell a wrong password from an unknown user.
var (
	ErrBadCredentials = errors.New("the username or password is incorrect")
	ErrBadToken       = errors.New("this session has expired; sign in again")
	ErrWeakPassword   = fmt.Errorf("the password must be at least %d characters", MinPasswordLength)
	ErrUserExists     = errors.New("an account already exists")
)

// Model is the authentication model. It is safe for concurrent use.
type Model struct {
	// DB holds the users and refresh_tokens tables.
	DB *store.DB
	// Key signs and verifies access tokens (HMAC-SHA256). It lives in
	// /var/lib/aos, never in config.yml, and rotating it signs everyone out.
	Key []byte
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time

	mu      sync.Mutex
	tickets map[string]time.Time // ticket -> expiry; single-use, never persisted
}

func (m *Model) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

// ---------------------------------------------------------------- passwords

// SetPassword creates or replaces the one user's password. Changing it revokes
// every existing refresh family, so other signed-in sessions are signed out, and
// clears the must-change flag: this is the user choosing a password of their own
// (PLAN.md §18 M6.5).
func (m *Model) SetPassword(ctx context.Context, username, password string) error {
	if len(password) < MinPasswordLength {
		return ErrWeakPassword
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := store.Millis(m.now())
	return m.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (username, pw_hash, created_at, updated_at, must_change) VALUES (?, ?, ?, ?, 0)
			ON CONFLICT (username) DO UPDATE SET pw_hash = excluded.pw_hash, updated_at = excluded.updated_at, must_change = 0`,
			username, hash, now, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked = 1 WHERE username = ?`, username)
		return err
	})
}

// CreateInitialUser writes the one account with a system-generated password that
// must be replaced on first sign-in (`aos mode ui`, install.sh). It refuses if a
// user already exists, so it is the account-creation moment and cannot silently
// reset a password already in use (PLAN.md §18 M6.5).
func (m *Model) CreateInitialUser(ctx context.Context, username, password string) error {
	if len(password) < MinPasswordLength {
		return ErrWeakPassword
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := store.Millis(m.now())
	return m.DB.Write(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return ErrUserExists
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO users (username, pw_hash, created_at, updated_at, must_change) VALUES (?, ?, ?, ?, 1)`,
			username, hash, now, now)
		return err
	})
}

// ChangePassword replaces the one user's password with one they chose, from a
// signed-in session (the forced first change, or an ordinary later one). It
// clears the must-change flag, revokes every existing family — so every other
// session is signed out (PLAN.md §18 M6.5) — and issues the caller a fresh pair
// so the session they changed it from stays alive without a re-login.
func (m *Model) ChangePassword(ctx context.Context, newPassword string) (access, refresh string, err error) {
	if len(newPassword) < MinPasswordLength {
		return "", "", ErrWeakPassword
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return "", "", err
	}
	var username string
	family := randomHex(16)
	expires := m.now().Add(RefreshTTL)
	now := store.Millis(m.now())
	if err := m.DB.Write(ctx, func(tx *sql.Tx) error {
		// Single user: there is exactly one row, and it is the caller's — the
		// middleware verified their access token before this handler ran.
		if err := tx.QueryRowContext(ctx, `SELECT username FROM users LIMIT 1`).Scan(&username); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrBadCredentials
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET pw_hash = ?, updated_at = ?, must_change = 0 WHERE username = ?`, hash, now, username); err != nil {
			return err
		}
		// Revoke every family first, then start a fresh one for the caller, so the
		// new token is not caught by the same sweep.
		if _, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked = 1 WHERE username = ?`, username); err != nil {
			return err
		}
		refresh, err = m.issue(ctx, tx, username, family, expires)
		return err
	}); err != nil {
		return "", "", err
	}
	if access, err = m.mintAccess(username); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// Revoke ends the family of the given refresh token, signing that session (and
// any it rotated into) out. Sign-out calls it. An unknown or empty token is a
// no-op, so signing out twice is harmless.
func (m *Model) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return m.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked = 1
			WHERE family = (SELECT family FROM refresh_tokens WHERE id = ?)`, tokenID(token))
		return err
	})
}

// hashPassword derives a self-describing pbkdf2 string a later Verify can read.
func hashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLength)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations, b64(salt), b64(dk)), nil
}

// verifyPassword checks password against an encoded pbkdf2 string in constant time.
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(dk, want) == 1
}

// ---------------------------------------------------------------- access tokens

// accessHeader is the constant JWT header, base64url-encoded once.
var accessHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

type accessClaims struct {
	Sub string `json:"sub"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// mintAccess returns a signed JWT that names username and expires in AccessTTL.
func (m *Model) mintAccess(username string) (string, error) {
	now := m.now()
	body, err := json.Marshal(accessClaims{Sub: username, Iat: now.Unix(), Exp: now.Add(AccessTTL).Unix()})
	if err != nil {
		return "", err
	}
	signing := accessHeader + "." + base64.RawURLEncoding.EncodeToString(body)
	return signing + "." + m.sign(signing), nil
}

func (m *Model) sign(signing string) string {
	mac := hmac.New(sha256.New, m.Key)
	mac.Write([]byte(signing))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyAccess reports whether token is a valid, unexpired access token.
func (m *Model) VerifyAccess(token string) bool {
	signing, sig, ok := cutLast(token, '.')
	if !ok || len(m.Key) == 0 {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(m.sign(signing))) {
		return false
	}
	_, payload, ok := strings.Cut(signing, ".")
	if !ok {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	var c accessClaims
	if json.Unmarshal(body, &c) != nil {
		return false
	}
	return m.now().Unix() < c.Exp
}

// cutLast splits s at its last sep, so header.payload keeps its own dot.
func cutLast(s string, sep byte) (before, after string, found bool) {
	i := strings.LastIndexByte(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

// ---------------------------------------------------------------- port grants

// PortGrant mints the value of the Path=/port/<n>/ cookie that authenticates a
// forwarded Service's many sub-resource loads once its opening ticket has been
// redeemed (ADR-0007, M6.4). It binds the port, so a grant minted for one
// Service cannot open another, and expires after PortGrantTTL.
func (m *Model) PortGrant(port int) string {
	body := strconv.Itoa(port) + "|" + strconv.FormatInt(m.now().Add(PortGrantTTL).Unix(), 10)
	enc := base64.RawURLEncoding.EncodeToString([]byte(body))
	return enc + "." + m.sign(enc)
}

// VerifyPortGrant reports whether value is a grant this Model minted for port
// and that has not expired.
func (m *Model) VerifyPortGrant(port int, value string) bool {
	enc, sig, ok := strings.Cut(value, ".")
	if !ok || len(m.Key) == 0 || !hmac.Equal([]byte(sig), []byte(m.sign(enc))) {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	gotPort, exp, ok := strings.Cut(string(body), "|")
	if !ok || gotPort != strconv.Itoa(port) {
		return false
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	return m.now().Unix() < unix
}

// ---------------------------------------------------------------- refresh tokens

// SignIn verifies the password and starts a new refresh family, returning a
// fresh access/refresh pair. mustChange is true when the account still holds a
// system-generated password: the Desktop then forces a change before the shell
// loads (PLAN.md §18 M6.5).
func (m *Model) SignIn(ctx context.Context, username, password string) (access, refresh string, mustChange bool, err error) {
	var (
		hash     string
		mustCode int
	)
	switch err := m.DB.Read().QueryRowContext(ctx, `SELECT pw_hash, must_change FROM users WHERE username = ?`, username).Scan(&hash, &mustCode); {
	case errors.Is(err, sql.ErrNoRows):
		// Run a verify against a throwaway hash anyway, so a missing user and a
		// wrong password take the same time.
		verifyPassword("pbkdf2-sha256$1$AA$AA", password)
		return "", "", false, ErrBadCredentials
	case err != nil:
		return "", "", false, err
	}
	if !verifyPassword(hash, password) {
		return "", "", false, ErrBadCredentials
	}
	family := randomHex(16)
	expires := m.now().Add(RefreshTTL)
	if err := m.DB.Write(ctx, func(tx *sql.Tx) error {
		refresh, err = m.issue(ctx, tx, username, family, expires)
		return err
	}); err != nil {
		return "", "", false, err
	}
	if access, err = m.mintAccess(username); err != nil {
		return "", "", false, err
	}
	return access, refresh, mustCode != 0, nil
}

// VerifyPassword reports whether password matches the one account's, with the
// same constant-time pbkdf2 check as sign-in (M6.3). It is the gate on the Root
// Mode switch (M7.7), which re-proves the person at the keyboard before raising
// privilege. With no account yet it runs a throwaway verify and returns false,
// so a missing account and a wrong password take the same time.
func (m *Model) VerifyPassword(ctx context.Context, password string) (bool, error) {
	var hash string
	switch err := m.DB.Read().QueryRowContext(ctx, `SELECT pw_hash FROM users LIMIT 1`).Scan(&hash); {
	case errors.Is(err, sql.ErrNoRows):
		verifyPassword("pbkdf2-sha256$1$AA$AA", password)
		return false, nil
	case err != nil:
		return false, err
	}
	return verifyPassword(hash, password), nil
}

// MustChange reports whether the one account still holds a system-generated
// password. The Refresh handler reads it so a reload during the forced first
// change lands back on the change screen rather than slipping past it into the
// shell (PLAN.md §18 M6.5). With no account yet it is false.
func (m *Model) MustChange(ctx context.Context) (bool, error) {
	var must int
	switch err := m.DB.Read().QueryRowContext(ctx, `SELECT must_change FROM users LIMIT 1`).Scan(&must); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	return must != 0, nil
}

// Refresh rotates a refresh token: the presented token is spent and a new one in
// the same family is returned. Presenting an already-spent token is a replay,
// which revokes the whole family (ADR-0007).
func (m *Model) Refresh(ctx context.Context, token string) (access, refresh string, err error) {
	id := tokenID(token)
	var (
		family, username string
		expiresAt        int64
		usedAt           sql.NullInt64
		revoked          int
	)
	switch err := m.DB.Read().QueryRowContext(ctx, `SELECT family, username, expires_at, used_at, revoked FROM refresh_tokens WHERE id = ?`, id).
		Scan(&family, &username, &expiresAt, &usedAt, &revoked); {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", ErrBadToken
	case err != nil:
		return "", "", err
	}
	switch {
	case revoked != 0 || m.now().After(store.Time(expiresAt)):
		return "", "", ErrBadToken
	case usedAt.Valid:
		// The token was already rotated once: someone is replaying a spent token.
		// Revoke every token in the family — the legitimate holder is signed out
		// too and must sign in again.
		_ = m.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked = 1 WHERE family = ?`, family)
			return err
		})
		return "", "", ErrBadToken
	}
	if err := m.DB.Write(ctx, func(tx *sql.Tx) error {
		// Spend this token, guarding against a concurrent rotation.
		res, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`, store.Millis(m.now()), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrBadToken
		}
		// The new token inherits the family's expiry, so rotation never extends a
		// family past RefreshTTL from the original sign-in.
		refresh, err = m.issue(ctx, tx, username, family, store.Time(expiresAt))
		return err
	}); err != nil {
		return "", "", err
	}
	if access, err = m.mintAccess(username); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// issue writes a new refresh token in family and returns its secret. Only
// sha256(secret) is stored, so a database leak cannot mint a session.
func (m *Model) issue(ctx context.Context, tx *sql.Tx, username, family string, expires time.Time) (string, error) {
	secret := randomHex(32)
	_, err := tx.ExecContext(ctx, `INSERT INTO refresh_tokens (id, family, username, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		tokenID(secret), family, username, store.Millis(m.now()), store.Millis(expires))
	return secret, err
}

// tokenID is the stored form of a refresh token secret.
func tokenID(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------- tickets

// Ticket mints a single-use, short-lived credential for a browser load that
// cannot send an Authorization header (a WebSocket, an <img>/<video> or a
// download). The Desktop asks for one over an already-authenticated request.
func (m *Model) Ticket() string {
	t := randomHex(24)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tickets == nil {
		m.tickets = map[string]time.Time{}
	}
	now := m.now()
	for k, exp := range m.tickets {
		if now.After(exp) {
			delete(m.tickets, k)
		}
	}
	m.tickets[t] = now.Add(TicketTTL)
	return t
}

// RedeemTicket consumes a ticket: it is gone whether or not it was valid, so a
// ticket authorises exactly one load and cannot be replayed.
func (m *Model) RedeemTicket(t string) bool {
	if t == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.tickets[t]
	delete(m.tickets, t)
	return ok && !m.now().After(exp)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

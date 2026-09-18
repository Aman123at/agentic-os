-- M6: the API is always authenticated, even on a public bind (ADR-0007). One
-- user signs in with a username and password; the password is stored as a
-- pbkdf2 hash, never in the clear. Refresh tokens live here so they survive a
-- restart and can be rotated on use, with a whole family revoked when a spent
-- token is replayed (PLAN.md §18 M6.3, §14).

CREATE TABLE users (
  username   TEXT PRIMARY KEY,
  -- pw_hash is a self-describing pbkdf2 string: pbkdf2-sha256$<iter>$<salt>$<key>.
  pw_hash    TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE refresh_tokens (
  -- id is sha256(secret): the token secret itself is never stored, so a database
  -- leak cannot mint a session.
  id         TEXT PRIMARY KEY,
  -- family ties every rotation of one sign-in together, so replaying a spent
  -- token can revoke them all at once.
  family     TEXT NOT NULL,
  username   TEXT NOT NULL REFERENCES users (username) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  -- used_at is set the moment a token is rotated; presenting a used token again
  -- is a replay and revokes the family.
  used_at    INTEGER,
  revoked    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX refresh_tokens_family ON refresh_tokens (family);

-- M6.5: a system-generated password — the one `aos mode ui` prints, or the one
-- install.sh writes into config.yml — must be replaced on the first sign-in, so
-- an account is never left on a secret the operator has seen in a terminal or a
-- file (PLAN.md §18 M6.5, ADR-0007). must_change is set when such an account is
-- created and cleared the first time the user sets a password of their own.
ALTER TABLE users ADD COLUMN must_change INTEGER NOT NULL DEFAULT 0;

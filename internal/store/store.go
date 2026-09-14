// Package store opens AOS's SQLite database (PLAN.md §14): WAL mode, one writer,
// migrations embedded in the binary.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB is the database. Writes go through a single connection, so they are
// serialised; reads use a pool.
type DB struct {
	w *sql.DB
	r *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", dsn+"&_pragma=query_only(1)")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	db := &DB{w: w, r: r}
	if err := db.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Close closes both connection pools.
func (db *DB) Close() error {
	return errorsJoin(db.r.Close(), db.w.Close())
}

// Read is the pool for queries.
func (db *DB) Read() *sql.DB { return db.r }

// Write runs fn in a write transaction.
func (db *DB) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (db *DB) migrate() error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	var version int
	if err := db.w.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i, name := range names {
		if i < version {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		err = db.Write(context.Background(), func(tx *sql.Tx) error {
			if _, err := tx.Exec(string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1))
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", strings.TrimPrefix(name, "migrations/"), err)
		}
	}
	return nil
}

// Millis converts t to the stored representation.
func Millis(t time.Time) int64 { return t.UnixMilli() }

// Time converts a stored time back.
func Time(ms int64) time.Time { return time.UnixMilli(ms) }

func errorsJoin(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

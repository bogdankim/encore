//go:build !darwin || !cgo

package musiccache

import "errors"

// Without cgo there is no libsqlite3, and so no reading Music's cache.
type DB struct{}

// Available reports whether this build can read SQLite (darwin with cgo).
const Available = false

var errNoSQLite = errors.New("sqlite: not available in this build")

func OpenDB(string, bool) (*DB, error) { return nil, errNoSQLite }

func (*DB) Close() {}

func (*DB) Exec(string) error { return errNoSQLite }

func (*DB) Query(string, []any, func([]any) error) error { return errNoSQLite }

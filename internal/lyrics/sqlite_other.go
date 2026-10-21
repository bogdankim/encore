//go:build !darwin || !cgo

package lyrics

import "errors"

// Without cgo there is no libsqlite3, and so no Apple Music lyrics.
type sqliteDB struct{}

const sqliteAvailable = false

var errNoSQLite = errors.New("sqlite: not available in this build")

func openSQLite(string, bool) (*sqliteDB, error) { return nil, errNoSQLite }

func (*sqliteDB) Close() {}

func (*sqliteDB) Exec(string) error { return errNoSQLite }

func (*sqliteDB) Query(string, []any, func([]any) error) error { return errNoSQLite }

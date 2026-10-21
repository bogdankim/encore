//go:build darwin && cgo

package musiccache

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>

// SQLITE_TRANSIENT is a macro over a function pointer cast, which cgo can't
// use directly.
static int encore_bind_text(sqlite3_stmt *st, int i, const char *s, int n) {
	return sqlite3_bind_text(st, i, s, n, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// DB is a minimal wrapper over the system libsqlite3: enough to read
// a few rows of Music's URL cache, and for tests to write a stand-in.
type DB struct{ db *C.sqlite3 }

// Available reports whether this build can read SQLite (darwin with cgo).
const Available = true

// OpenDB opens a database by URI ("file:/path?mode=ro").
func OpenDB(uri string, readOnly bool) (*DB, error) {
	curi := C.CString(uri)
	defer C.free(unsafe.Pointer(curi))
	flags := C.int(C.SQLITE_OPEN_URI | C.SQLITE_OPEN_NOMUTEX)
	if readOnly {
		flags |= C.SQLITE_OPEN_READONLY
	} else {
		flags |= C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE
	}
	var db *C.sqlite3
	rc := C.sqlite3_open_v2(curi, &db, flags, nil)
	if rc != C.SQLITE_OK {
		err := sqliteErr(db, rc)
		C.sqlite3_close_v2(db)
		return nil, err
	}
	C.sqlite3_busy_timeout(db, 250)
	return &DB{db: db}, nil
}

func (d *DB) Close() {
	if d != nil && d.db != nil {
		C.sqlite3_close_v2(d.db)
		d.db = nil
	}
}

func sqliteErr(db *C.sqlite3, rc C.int) error {
	msg := C.GoString(C.sqlite3_errstr(rc))
	if db != nil {
		msg = C.GoString(C.sqlite3_errmsg(db))
	}
	return fmt.Errorf("sqlite: %s (%d)", msg, int(rc))
}

// Exec runs statements that return no rows.
func (d *DB) Exec(sql string) error {
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	if rc := C.sqlite3_exec(d.db, csql, nil, nil, nil); rc != C.SQLITE_OK {
		return sqliteErr(d.db, rc)
	}
	return nil
}

// Query runs one statement with args (int64 or string) and calls row
// for each result row. Values are int64, float64, []byte (TEXT and BLOB,
// copied) or nil.
func (d *DB) Query(sql string, args []any, row func(vals []any) error) error {
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	var st *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(d.db, csql, -1, &st, nil); rc != C.SQLITE_OK {
		return sqliteErr(d.db, rc)
	}
	defer C.sqlite3_finalize(st)
	for i, a := range args {
		var rc C.int
		switch v := a.(type) {
		case int64:
			rc = C.sqlite3_bind_int64(st, C.int(i+1), C.sqlite3_int64(v))
		case string:
			cs := C.CString(v)
			rc = C.encore_bind_text(st, C.int(i+1), cs, C.int(len(v)))
			C.free(unsafe.Pointer(cs))
		default:
			return fmt.Errorf("sqlite: unsupported argument %T", a)
		}
		if rc != C.SQLITE_OK {
			return sqliteErr(d.db, rc)
		}
	}
	n := int(C.sqlite3_column_count(st))
	for {
		switch rc := C.sqlite3_step(st); rc {
		case C.SQLITE_DONE:
			return nil
		case C.SQLITE_ROW:
		default:
			return sqliteErr(d.db, rc)
		}
		vals := make([]any, n)
		for i := range n {
			ci := C.int(i)
			switch C.sqlite3_column_type(st, ci) {
			case C.SQLITE_INTEGER:
				vals[i] = int64(C.sqlite3_column_int64(st, ci))
			case C.SQLITE_FLOAT:
				vals[i] = float64(C.sqlite3_column_double(st, ci))
			case C.SQLITE_NULL:
				vals[i] = nil
			default: // TEXT, BLOB
				p := C.sqlite3_column_blob(st, ci)
				size := C.sqlite3_column_bytes(st, ci)
				vals[i] = C.GoBytes(p, size)
			}
		}
		if err := row(vals); err != nil {
			return err
		}
	}
}

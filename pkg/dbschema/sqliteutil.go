package dbschema

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// CreateSQLiteDatabase creates or opens a SQLite database at the given path and executes the provided statements.
func CreateSQLiteDatabase(dbPath string, statements []string) error {
	cpath := C.CString(dbPath)
	defer C.free(unsafe.Pointer(cpath))

	var db *C.sqlite3
	if rc := C.sqlite3_open_v2(cpath, &db, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE, nil); rc != C.SQLITE_OK {
		return fmt.Errorf("open sqlite: %s", C.GoString(C.sqlite3_errmsg(db)))
	}
	defer C.sqlite3_close(db)

	for _, stmt := range statements {
		cstmt := C.CString(stmt)
		rc := C.sqlite3_exec(db, cstmt, nil, nil, nil)
		C.free(unsafe.Pointer(cstmt))
		if rc != C.SQLITE_OK {
			return fmt.Errorf("exec stmt: %s", C.GoString(C.sqlite3_errmsg(db)))
		}
	}

	return nil
}

// Package dbschema compares SQLite schemas against expected DDL files.
package dbschema

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unsafe"
)

// Table describes a database table and its columns.
type Table struct {
	Name    string
	Columns map[string]string // column name -> type
}

var createTableRegex = regexp.MustCompile(`(?is)create\s+table\s+(?:if\s+not\s+exists\s+)?([\w"` + "`" + `\[\]]+)\s*\((.*?)\);`)

// ParseExpectedSchema parses a DDL definition and extracts table definitions.
func ParseExpectedSchema(r io.Reader) (map[string]Table, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read expected schema: %w", err)
	}

	ddl := string(content)
	matches := createTableRegex.FindAllStringSubmatch(ddl, -1)
	tables := make(map[string]Table)

	for _, m := range matches {
		name := normalizeIdent(m[1])
		if name == "" {
			continue
		}

		columnSection := m[2]
		columns := parseColumns(columnSection)
		tables[name] = Table{Name: name, Columns: columns}
	}

	return tables, nil
}

// LoadActualSchema introspects a SQLite database file to extract table and column info.
func LoadActualSchema(ctx context.Context, dbPath string) (map[string]Table, error) {
	_ = ctx

	cpath := C.CString(dbPath)
	defer C.free(unsafe.Pointer(cpath))

	var db *C.sqlite3
	if rc := C.sqlite3_open_v2(cpath, &db, C.SQLITE_OPEN_READONLY, nil); rc != C.SQLITE_OK {
		msg := C.GoString(C.sqlite3_errmsg(db))
		if db != nil {
			C.sqlite3_close(db)
		}
		return nil, fmt.Errorf("open sqlite db: %s", msg)
	}
	defer C.sqlite3_close(db)

	tableNames, err := querySingleColumn(db, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}

	tables := make(map[string]Table)
	for _, name := range tableNames {
		cols, err := loadColumns(db, name)
		if err != nil {
			return nil, err
		}
		tables[name] = Table{Name: name, Columns: cols}
	}

	return tables, nil
}

func loadColumns(db *C.sqlite3, table string) (map[string]string, error) {
	q := fmt.Sprintf("PRAGMA table_info('%s')", strings.ReplaceAll(table, "'", "''"))
	rows, err := queryColumns(db, q)
	if err != nil {
		return nil, fmt.Errorf("load columns for %s: %w", table, err)
	}

	cols := make(map[string]string)
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		name := row[1]
		colType := strings.ToUpper(strings.TrimSpace(row[2]))
		cols[name] = colType
	}

	return cols, nil
}

func querySingleColumn(db *C.sqlite3, query string) ([]string, error) {
	rows, err := queryColumns(db, query)
	if err != nil {
		return nil, err
	}

	vals := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) > 0 {
			vals = append(vals, row[0])
		}
	}
	return vals, nil
}

func queryColumns(db *C.sqlite3, query string) ([][]string, error) {
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))

	var stmt *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(db, cquery, -1, &stmt, nil); rc != C.SQLITE_OK {
		return nil, fmt.Errorf("prepare query: %s", C.GoString(C.sqlite3_errmsg(db)))
	}
	defer C.sqlite3_finalize(stmt)

	colCount := int(C.sqlite3_column_count(stmt))
	var rows [][]string

	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_ROW {
			row := make([]string, colCount)
			for i := 0; i < colCount; i++ {
				text := (*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i))))
				if text != nil {
					row[i] = C.GoString(text)
				}
			}
			rows = append(rows, row)
		} else if rc == C.SQLITE_DONE {
			break
		} else {
			return nil, fmt.Errorf("step query: %s", C.GoString(C.sqlite3_errmsg(db)))
		}
	}

	return rows, nil
}

func parseColumns(section string) map[string]string {
	parts := splitColumns(section)
	columns := make(map[string]string)

	for _, raw := range parts {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "PRIMARY ") || strings.HasPrefix(upper, "FOREIGN ") || strings.HasPrefix(upper, "UNIQUE ") || strings.HasPrefix(upper, "CHECK ") || strings.HasPrefix(upper, "CONSTRAINT") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		name := normalizeIdent(fields[0])
		if name == "" {
			continue
		}

		colType := ""
		if len(fields) > 1 {
			colType = strings.ToUpper(fields[1])
		}

		columns[name] = colType
	}

	return columns
}

func splitColumns(section string) []string {
	var parts []string
	var sb strings.Builder
	depth := 0

	for _, r := range section {
		switch r {
		case '(':
			depth++
			sb.WriteRune(r)
		case ')':
			if depth > 0 {
				depth--
			}
			sb.WriteRune(r)
		case ',':
			if depth == 0 {
				parts = append(parts, sb.String())
				sb.Reset()
				continue
			}
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	if sb.Len() > 0 {
		parts = append(parts, sb.String())
	}

	return parts
}

func normalizeIdent(name string) string {
	n := strings.TrimSpace(name)
	n = strings.Trim(n, "`"+"\"[]")
	return strings.ToLower(n)
}

func compareTypes(expected, actual string) bool {
	return strings.EqualFold(expected, actual)
}

// CompareSchemas compares expected vs actual schema and returns findings.
func CompareSchemas(expected, actual map[string]Table) []Result {
	return diffSchemas(expected, actual)
}

// Result represents a schema drift finding.
type Result struct {
	RuleID string
	Level  string
	Text   string
}

func diffSchemas(expected, actual map[string]Table) []Result {
	var results []Result

	for name, exp := range expected {
		act, ok := actual[name]
		if !ok {
			results = append(results, Result{RuleID: "db-schema-missing-table", Level: "error", Text: fmt.Sprintf("Missing table '%s'", name)})
			continue
		}

		for col, expType := range exp.Columns {
			actType, ok := act.Columns[col]
			if !ok {
				results = append(results, Result{RuleID: "db-schema-missing-column", Level: "error", Text: fmt.Sprintf("Missing column '%s.%s'", name, col)})
				continue
			}

			if expType != "" && actType != "" && !compareTypes(expType, actType) {
				results = append(results, Result{RuleID: "db-schema-type-mismatch", Level: "warning", Text: fmt.Sprintf("Type mismatch for column '%s.%s': expected %s, found %s", name, col, expType, actType)})
			}
		}

		for col := range act.Columns {
			if _, ok := exp.Columns[col]; !ok {
				results = append(results, Result{RuleID: "db-schema-extra-column", Level: "warning", Text: fmt.Sprintf("Extra column '%s.%s'", name, col)})
			}
		}
	}

	for name := range actual {
		if _, ok := expected[name]; !ok {
			results = append(results, Result{RuleID: "db-schema-extra-table", Level: "warning", Text: fmt.Sprintf("Extra table '%s'", name)})
		}
	}

	return results
}

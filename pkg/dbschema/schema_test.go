package dbschema

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareSchemas_Match(t *testing.T) {
	ddl := `CREATE TABLE users (
  id INTEGER,
  name TEXT
);`

	expected := parseDDL(t, ddl)
	dbPath := createDB(t, []string{ddl})

	actual, err := LoadActualSchema(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	findings := CompareSchemas(expected, actual)
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestCompareSchemas_MissingTable(t *testing.T) {
	ddl := `CREATE TABLE users (id INTEGER); CREATE TABLE orders (id INTEGER);`
	expected := parseDDL(t, ddl)
	dbPath := createDB(t, []string{"CREATE TABLE users (id INTEGER);"})

	actual, err := LoadActualSchema(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	findings := CompareSchemas(expected, actual)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "db-schema-missing-table" {
		t.Fatalf("unexpected rule: %s", findings[0].RuleID)
	}
}

func TestCompareSchemas_MissingColumn(t *testing.T) {
	ddl := `CREATE TABLE users (
  id INTEGER,
  name TEXT,
  email TEXT
);`
	expected := parseDDL(t, ddl)
	dbPath := createDB(t, []string{"CREATE TABLE users (id INTEGER, name TEXT);"})

	actual, err := LoadActualSchema(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	findings := CompareSchemas(expected, actual)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "db-schema-missing-column" {
		t.Fatalf("unexpected rule: %s", findings[0].RuleID)
	}
}

func TestCompareSchemas_ExtraTable(t *testing.T) {
	ddl := `CREATE TABLE users (id INTEGER);`
	expected := parseDDL(t, ddl)
	dbPath := createDB(t, []string{"CREATE TABLE users (id INTEGER);", "CREATE TABLE extras (id INTEGER);"})

	actual, err := LoadActualSchema(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	findings := CompareSchemas(expected, actual)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "db-schema-extra-table" {
		t.Fatalf("unexpected rule: %s", findings[0].RuleID)
	}
}

func parseDDL(t *testing.T, ddl string) map[string]Table {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "expected-*.sql")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.WriteString(ddl); err != nil {
		t.Fatalf("write ddl: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	tables, err := ParseExpectedSchema(f)
	if err != nil {
		t.Fatalf("parse expected: %v", err)
	}
	return tables
}

func createDB(t *testing.T, stmts []string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.sqlite")

	if err := CreateSQLiteDatabase(dbPath, stmts); err != nil {
		t.Fatalf("create db: %v", err)
	}

	return dbPath
}

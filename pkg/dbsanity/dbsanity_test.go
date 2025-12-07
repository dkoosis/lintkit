package dbsanity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestCheckDatabaseNoFindings(t *testing.T) {
	dbPath := tempDB(t)
	createTable(t, dbPath, "nugs", 3)

	baseline := Baseline{Tables: map[string]int64{"nugs": 3}}

	results, err := CheckDatabase(context.Background(), dbPath, baseline, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected no findings, got %d", len(results))
	}
}

func TestCheckDatabaseDetectsDrop(t *testing.T) {
	dbPath := tempDB(t)
	createTable(t, dbPath, "tags", 1)

	baseline := Baseline{Tables: map[string]int64{"tags": 10}}

	results, err := CheckDatabase(context.Background(), dbPath, baseline, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected one finding, got %d", len(results))
	}

	if results[0].RuleID != "db-row-drift" {
		t.Fatalf("unexpected rule ID: %s", results[0].RuleID)
	}
}

func TestCheckDatabaseIgnoresNewTables(t *testing.T) {
	dbPath := tempDB(t)
	createTable(t, dbPath, "relations", 5)
	createTable(t, dbPath, "extra", 7)

	baseline := Baseline{Tables: map[string]int64{"relations": 5}}

	results, err := CheckDatabase(context.Background(), dbPath, baseline, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected no findings for new tables, got %d", len(results))
	}
}

func TestLoadBaseline(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "baseline-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer tmp.Close()

	baseline := Baseline{Tables: map[string]int64{"nugs": 100}}
	if err := json.NewEncoder(tmp).Encode(baseline); err != nil {
		t.Fatalf("failed to write baseline: %v", err)
	}

	loaded, err := LoadBaseline(tmp.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loaded.Tables["nugs"] != 100 {
		t.Fatalf("unexpected baseline value: %d", loaded.Tables["nugs"])
	}
}

func tempDB(t *testing.T) string {
	t.Helper()

	tmp, err := os.CreateTemp(t.TempDir(), "dbsanity-*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	tmp.Close()
	return tmp.Name()
}

func createTable(t *testing.T, dbPath string, name string, rows int) {
	t.Helper()

	ddl := fmt.Sprintf("CREATE TABLE %s (id INTEGER PRIMARY KEY, value TEXT);", name)
	if err := exec.Command("sqlite3", dbPath, ddl).Run(); err != nil {
		t.Fatalf("failed to create table %s: %v", name, err)
	}

	for i := 0; i < rows; i++ {
		insert := fmt.Sprintf("INSERT INTO %s (value) VALUES ('v');", name)
		if err := exec.Command("sqlite3", dbPath, insert).Run(); err != nil {
			t.Fatalf("failed to insert row: %v", err)
		}
	}
}

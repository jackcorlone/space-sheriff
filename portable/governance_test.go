package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStoreMigratesV1DatabaseToCurrentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	v1 := &Store{db: db, path: path}
	db.SetMaxOpenConns(1)
	if err := v1.configure(); err != nil {
		t.Fatal(err)
	}
	if err := v1.migrateV1(); err != nil {
		t.Fatal(err)
	}
	if _, err := v1.db.Exec(
		`INSERT INTO cleanup_transactions(
		 id, state, started_at, completed_at, planned_bytes, released_bytes)
		 VALUES('legacy', 'completed', 1, 2, 42, 42)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := v1.db.Exec(
		`INSERT INTO cleanup_items(
		 transaction_id, path, identity, size, modified_at, state)
		 VALUES('legacy', '/legacy.bin', 'legacy-id', 42, 1, 'trashed')`,
	); err != nil {
		t.Fatal(err)
	}
	if err := v1.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	state, err := store.policies()
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion || state.ActiveID != "balanced" || len(state.Policies) != 3 {
		t.Fatalf("unexpected migrated state: version=%d policies=%+v", version, state)
	}
	audit, err := store.auditDetail("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if audit.PolicyID != "balanced" || audit.PolicyVersion != 1 ||
		len(audit.Items) != 1 || audit.Items[0].Path != "/legacy.bin" {
		t.Fatalf("legacy audit was not preserved: %+v", audit)
	}
}

func TestStoreImportsActivatesAndPersistsPolicy(t *testing.T) {
	store, path := testStore(t)
	custom := balancedPolicy()
	custom.ID = "team-policy"
	custom.Name = "团队策略"
	custom.Version = 4
	custom.BuiltIn = false
	if err := store.importPolicy(custom); err != nil {
		t.Fatal(err)
	}
	if err := store.activatePolicy(custom.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.importPolicy(custom); err == nil {
		t.Fatal("policy version rollback was accepted")
	}
	if err := store.importPolicy(balancedPolicy()); err == nil {
		t.Fatal("built-in policy overwrite was accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	active, err := reopened.activePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != custom.ID || active.Version != custom.Version {
		t.Fatalf("unexpected active policy: %+v", active)
	}
}

func TestStoreAuditAndMaintenance(t *testing.T) {
	store, _ := testStore(t)
	record := FileRecord{
		Path: "/candidate.bin", Identity: "identity", Size: 42, ModifiedUnixNano: 123,
	}
	transactionID, err := store.beginCleanup([]FileRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.markCleanupItem(transactionID, record.Path, "trashed", "", record.Size); err != nil {
		t.Fatal(err)
	}
	if err := store.finishCleanup(transactionID, "completed"); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.auditSummaries(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].TrashedCount != 1 ||
		summaries[0].PolicyID != "balanced" {
		t.Fatalf("unexpected audit summaries: %+v", summaries)
	}
	detail, err := store.auditDetail(transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Items) != 1 || detail.Items[0].State != "trashed" {
		t.Fatalf("unexpected audit detail: %+v", detail)
	}
	health, err := store.databaseHealth()
	if err != nil {
		t.Fatal(err)
	}
	if health.SchemaVersion != schemaVersion || health.CleanupTransactions != 1 || health.Integrity != "ok" {
		t.Fatalf("unexpected database health: %+v", health)
	}
	if err := store.maintain("checkpoint"); err != nil {
		t.Fatal(err)
	}
	if err := store.maintain("optimize"); err != nil {
		t.Fatal(err)
	}
	if err := store.maintain("erase"); err == nil {
		t.Fatal("unknown maintenance action was accepted")
	}
}

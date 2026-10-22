package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedMigrations(t *testing.T) {
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		t.Fatalf("failed to read embedded migrations: %v", err)
	}

	var sqlFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			sqlFiles = append(sqlFiles, entry.Name())
		}
	}

	if len(sqlFiles) < 6 {
		t.Fatalf("expected at least 6 migration files, found %d: %v", len(sqlFiles), sqlFiles)
	}

	expectedPrefixes := []string{"000001_", "000002_", "000003_", "000004_", "000005_", "000006_"}
	for i, prefix := range expectedPrefixes {
		if !strings.HasPrefix(sqlFiles[i], prefix) {
			t.Errorf("migration %d expected prefix %s, got %s", i, prefix, sqlFiles[i])
		}

		content, err := migrationFiles.ReadFile(sqlFiles[i])
		if err != nil {
			t.Fatalf("failed reading migration file %s: %v", sqlFiles[i], err)
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			t.Errorf("migration file %s is empty", sqlFiles[i])
		}
	}
}


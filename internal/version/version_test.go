package version_test

import (
	"strings"
	"testing"

	"syn/internal/version"
)

func TestVersion_MetadataAndSummary(t *testing.T) {
	t.Parallel()

	if version.CurrentVersion == "" {
		t.Fatal("expected non-empty CurrentVersion")
	}
	if !strings.HasPrefix(version.GitRepositoryURL, "https://github.com/") {
		t.Fatalf("expected github URL, got %s", version.GitRepositoryURL)
	}

	summary := version.FormatSummary()
	if !strings.Contains(summary, version.CurrentVersion) {
		t.Fatalf("expected summary to contain version %s, got: %s", version.CurrentVersion, summary)
	}
	if !strings.Contains(summary, version.GitRepositoryURL) {
		t.Fatalf("expected summary to contain github url %s, got: %s", version.GitRepositoryURL, summary)
	}
	if len(version.History) < 3 {
		t.Fatalf("expected at least 3 historical releases, got %d", len(version.History))
	}
}


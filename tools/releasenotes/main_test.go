package main

import (
	"strings"
	"testing"
)

func TestChangelogSectionSelectsExactRelease(t *testing.T) {
	changelog := "# Changelog\n\n## 1.2.0 - today\n\n- New lifecycle view.\n\n## 1.1.0 - yesterday\n\n- Older change.\n"
	section, err := changelogSection(changelog, "1.2.0")
	if err != nil {
		t.Fatalf("extract release notes section: %v", err)
	}
	if section != "- New lifecycle view." {
		t.Fatalf("expected current release bullet only, received %q", section)
	}
}

func TestChangelogSectionRejectsMissingOrEmptyRelease(t *testing.T) {
	for _, changelog := range []string{"# Changelog\n", "## 1.2.0\n\n## 1.1.0\n- older\n"} {
		if _, err := changelogSection(changelog, "1.2.0"); err == nil || !strings.Contains(err.Error(), "1.2.0") {
			t.Fatalf("expected actionable release section error, received %v", err)
		}
	}
}

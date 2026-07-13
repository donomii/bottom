package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	changelogPath    = "CHANGELOG.md"
	releaseNotesPath = "dist/release-notes.md"
)

func main() {
	ref := os.Getenv("GITHUB_REF_NAME")
	notes, err := releaseNotes(ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate release notes for ref %q: %v\n", ref, err)
		os.Exit(1)
	}
	if err := os.WriteFile(releaseNotesPath, []byte(notes), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write release notes to %q for ref %q: %v\n", releaseNotesPath, ref, err)
		os.Exit(1)
	}
}

func releaseNotes(ref string) (string, error) {
	if !strings.HasPrefix(ref, "v") || len(ref) == 1 {
		return "", fmt.Errorf("expected a v-prefixed release tag, received %q", ref)
	}
	contents, err := os.ReadFile(changelogPath)
	if err != nil {
		return "", fmt.Errorf("read changelog %q: %w", changelogPath, err)
	}
	section, err := changelogSection(string(contents), strings.TrimPrefix(ref, "v"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# bottom %s\n\n%s\n\nEvery archive is accompanied by an SPDX JSON software bill of materials. SHA-256 checksums and GitHub build provenance cover the published release artifacts.\n", ref, section), nil
}

func changelogSection(changelog string, version string) (string, error) {
	lines := strings.Split(changelog, "\n")
	start := -1
	for index, line := range lines {
		if strings.HasPrefix(line, "## "+version+" ") || line == "## "+version {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("find changelog section for version %q: expected heading beginning %q", version, "## "+version)
	}
	end := len(lines)
	for index := start; index < len(lines); index++ {
		if strings.HasPrefix(lines[index], "## ") {
			end = index
			break
		}
	}
	section := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	if section == "" {
		return "", fmt.Errorf("find changelog section for version %q: section is empty", version)
	}
	return section, nil
}

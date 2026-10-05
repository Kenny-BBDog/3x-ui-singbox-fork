package main

// REVIEW.md briefs a reviewer on which CI job proves what. A stale claim there
// is invisible (the reviewer trusts it), so the facts it states are pinned here.
//
// This file used to also pin .github/claude/issue-analyst-context.md, the prompt
// for the upstream Claude issue-analyst workflow. That workflow and its prompt
// were removed from this fork, so those tests went with them.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	reviewPath     = "REVIEW.md"
	ciWorkflowPath = ".github/workflows/ci.yml"
)

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// REVIEW.md tells the reviewer which CI job proves what, and which skip gates
// mean a green run proved nothing. Both go stale silently on a rename.
func TestReviewNamesRealCIJobsAndGates(t *testing.T) {
	doc := readRepoFile(t, reviewPath)
	ci := readRepoFile(t, ciWorkflowPath)
	// Hyphenated only: a single-word job name is indistinguishable from prose.
	jobs := regexp.MustCompile("`([a-z0-9]+(?:-[a-z0-9]+)+)`").FindAllStringSubmatch(doc, -1)
	if len(jobs) < 2 {
		t.Fatalf("expected %s to name at least 2 CI jobs in backticks, found %d", reviewPath, len(jobs))
	}
	for _, j := range jobs {
		t.Run(j[1], func(t *testing.T) {
			if !strings.Contains(ci, "\n  "+j[1]+":\n") {
				t.Errorf("%s names a CI job %q that %s does not define", reviewPath, j[1], ciWorkflowPath)
			}
		})
	}
	for _, g := range regexp.MustCompile("`((?:XUI|XRAY)_[A-Z0-9_]+)`").FindAllStringSubmatch(doc, -1) {
		t.Run(g[1], func(t *testing.T) {
			if strings.Contains(ci, g[1]) {
				t.Errorf("%s claims %s is never set in CI, but %s sets it", reviewPath, g[1], ciWorkflowPath)
			}
		})
	}
}

// The i18n rule is the one REVIEW.md states as a number, so it is the one that
// goes wrong silently when a locale is added.
func TestReviewLocaleFileCount(t *testing.T) {
	doc := readRepoFile(t, reviewPath)
	m := regexp.MustCompile(`(\d+) locale files`).FindStringSubmatch(doc)
	if m == nil {
		t.Fatalf("%s no longer states the i18n rule as \"N locale files\"", reviewPath)
	}
	files, err := filepath.Glob("internal/web/translation/*.json")
	if err != nil {
		t.Fatalf("glob locales: %v", err)
	}
	if got := len(files); m[1] != itoa(got) {
		t.Errorf("%s tells the reviewer to expect %s locale files, internal/web/translation/ holds %d", reviewPath, m[1], got)
	}
}

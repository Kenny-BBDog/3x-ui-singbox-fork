package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tempLeaks(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".atomic-*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return matches
}

// Catches a write that is not actually atomic: the content and mode must land,
// and the temp file must not outlive the call.
func TestWriteReplacesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q, want %q", data, "new")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Windows has no Unix permission bits, so the mode guarantee can only be
	// asserted where those bits exist (CI is Linux).
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode = %o, want 600", got)
		}
	}
	if leaks := tempLeaks(t, dir); len(leaks) != 0 {
		t.Fatalf("temp files leaked: %v", leaks)
	}
}

// Catches the failure this package exists to prevent: a write that fails midway
// must leave the previously committed file intact, not truncated or removed.
// A node reads its certificate at process start, so a half-written key here is
// a node-wide outage rather than a failed write.
func TestWriteKeepsPreviousContentWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "privkey.pem")
	if err := Write(path, []byte("committed"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}

	original := rename
	rename = func(_, _ string) error { return errors.New("injected rename failure") }
	t.Cleanup(func() { rename = original })

	if err := Write(path, []byte("partial"), 0o600); err == nil {
		t.Fatal("Write succeeded despite a failing rename")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved file: %v", err)
	}
	if string(data) != "committed" {
		t.Fatalf("content after failed rename = %q, want the committed value", data)
	}
	if leaks := tempLeaks(t, dir); len(leaks) != 0 {
		t.Fatalf("temp files leaked: %v", leaks)
	}
}

// Catches a write that creates a destination it cannot actually fill: when the
// directory is missing the call must fail without leaving anything behind.
func TestWriteMissingDirectoryLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent", "cert.pem")

	if err := Write(path, []byte("x"), 0o600); err == nil {
		t.Fatal("Write into a missing directory returned nil")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("destination exists after a failed write: stat err = %v", err)
	}
	if leaks := tempLeaks(t, dir); len(leaks) != 0 {
		t.Fatalf("temp files leaked: %v", leaks)
	}
}

// Package atomicfile replaces a file without ever exposing a partial write.
package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
)

// rename is a variable so a test can inject a failure and prove that the
// previously committed file survives it.
var rename = os.Rename

// Write replaces path with data atomically. The temp file is created in the
// destination directory, since rename is atomic only within a filesystem.
func Write(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".atomic-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	// Data before rename, directory entry after it: a crash in between must not
	// leave the new content visible but unpersisted.
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = rename(tmpPath, path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// Directory fsync is unsupported here, and os.Rename already replaces.
		return nil
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = dirHandle.Sync()
	_ = dirHandle.Close()
	return err
}

package migration

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

func prepareStorage(paths config.Paths, source string) error {
	if _, err := logstore.EnsureSubdir(paths.Directory, "logs"); err != nil {
		return err
	}
	// Resolve parent symlinks as well as the selected roots before copying.
	resolved, err := filepath.EvalSymlinks(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	destination, err := filepath.EvalSymlinks(paths.LogDirectory)
	if err != nil {
		return err
	}
	if overlaps(resolved, destination) {
		return fmt.Errorf("source resolves inside the destination storage; nothing was removed")
	}
	return nil
}

func lockMigration(directory string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join(directory, ".migration.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another migration is running: %w", err)
	}
	return lock, nil
}

func cleanupSources(sources []string, legacyDirectory string) (bool, error) {
	remaining := false
	for _, source := range sources {
		if err := removeEmptyTree(source); err != nil {
			return remaining, err
		}
		if _, err := os.Lstat(source); err == nil {
			remaining = true
		} else if !os.IsNotExist(err) {
			return remaining, err
		}
	}
	// Only remove the known legacy application directory when it is empty.
	if err := os.Remove(legacyDirectory); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
		return remaining, err
	}
	return remaining, nil
}

func overlaps(a, b string) bool {
	within := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func removeEmptyTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if e.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
			return err
		}
	}
	return nil
}

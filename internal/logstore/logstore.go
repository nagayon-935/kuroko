// Package logstore enumerates kuroko session log files on disk. It is the
// single source of truth for "what counts as a saved log", shared by the
// viewer's log selector and the shell-completion candidate generator.
package logstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ListLogFiles enumerates regular .log/.log.gz files recursively, without
// following symlinks. Name returns a path relative to dir, so every caller
// (selector, completion, rotation, latest) uses the same unambiguous paths.
func ListLogFiles(dir string) ([]os.DirEntry, error) {
	var logs []os.DirEntry
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !IsLogName(entry.Name()) || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		logs = append(logs, relativeEntry{DirEntry: entry, name: rel})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading log dir: %w", err)
	}
	return logs, nil
}

type relativeEntry struct {
	os.DirEntry
	name string
}

func (e relativeEntry) Name() string { return e.name }

func IsLogName(name string) bool {
	return strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz")
}

// ResolveLogFile prefers an existing cwd path, then a root-relative path.
// Bare names may match nested logs; ambiguity is reported with candidates.
// Explicit ./, ../, absolute paths and unstat-able cwd paths keep precedence.
func ResolveLogFile(arg string, logDir func() (string, error)) (string, error) {
	cleaned := filepath.Clean(arg)
	if filepath.IsAbs(arg) || cleaned != arg || arg == "." || arg == ".." || strings.HasPrefix(arg, ".."+string(filepath.Separator)) || !isNotExist(arg) {
		return cleaned, nil
	}
	dir, err := logDir()
	if err != nil {
		return "", fmt.Errorf("resolving log dir: %w", err)
	}
	candidate := filepath.Join(dir, arg)
	if _, err := os.Lstat(candidate); err == nil {
		return candidate, nil
	}
	if filepath.Base(arg) != arg {
		return cleaned, nil
	}
	entries, err := ListLogFiles(dir)
	if errors.Is(err, os.ErrNotExist) {
		return cleaned, nil
	}
	if err != nil {
		return "", err
	}
	var matches []string
	for _, entry := range entries {
		if filepath.Base(entry.Name()) == arg {
			matches = append(matches, entry.Name())
		}
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous log name %q; specify one of: %s", arg, strings.Join(matches, ", "))
	}
	if len(matches) == 1 {
		return filepath.Join(dir, matches[0]), nil
	}
	return cleaned, nil
}

// isNotExist reports whether path definitely does not exist (as opposed to
// existing or being unstat-able for some other reason).
func isNotExist(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

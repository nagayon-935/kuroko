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

// ListLogFiles returns the log files in dir, sorted by directory-entry name
// (os.ReadDir order). A file qualifies if it is not a directory and its name
// ends in ".log" or ".log.gz".
func ListLogFiles(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading log dir: %w", err)
	}

	var logs []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".log.gz") {
			continue
		}
		logs = append(logs, entry)
	}
	return logs, nil
}

// ResolveLogFile maps a `kuroko view` argument to a file path.
//
// Only a bare file name (arg as typed has no directory component, so
// "./x" and "sub/../x" are excluded) is eligible for the log-dir fallback,
// and only when nothing named arg exists in the working directory. Any
// other stat outcome — the file exists, is a dangling symlink, or can't be
// checked (permission, ELOOP) — keeps arg so the caller reports the real
// open error instead of silently opening a different log.
//
// logDir is called only when the fallback is needed; its error is
// returned so a broken config isn't masked as "no such file". Returns the
// cleaned arg when no log-dir file matches.
func ResolveLogFile(arg string, logDir func() (string, error)) (string, error) {
	cleaned := filepath.Clean(arg)
	if filepath.Base(arg) != arg || !isNotExist(arg) {
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
	return cleaned, nil
}

// isNotExist reports whether path definitely does not exist (as opposed to
// existing or being unstat-able for some other reason).
func isNotExist(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

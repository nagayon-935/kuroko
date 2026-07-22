// Package logstore enumerates kuroko session log files on disk. It is the
// single source of truth for "what counts as a saved log", shared by the
// viewer's log selector and the shell-completion candidate generator.
package logstore

import (
	"fmt"
	"os"
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

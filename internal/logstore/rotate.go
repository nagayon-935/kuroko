package logstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

// abandonedLogAge is how long a log without a completion footer must have
// been untouched before retention cleanup treats it as abandoned (its session
// was killed or crashed) rather than as a session still running under an
// older kuroko that did not lock its log.
const abandonedLogAge = 24 * time.Hour

// Match generateFilename's sanitized command and optional target, including
// the numeric suffix createLogFile adds on collisions.
const filenamePart = `[^/\\: *?"<>|\p{Cc}\p{Cf}]{1,96}`

var sessionLogName = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_` + filenamePart + `(_` + filenamePart + `)?(_[1-9][0-9]*)?\.log(\.gz)?$`)

// IsSessionLogName reports whether name looks like a log file written by a
// kuroko session (see logger's filename generation), as opposed to a foreign
// .log file that happens to live in the log directory. RotateLogs only ever
// removes files that match.
func IsSessionLogName(name string) bool { return sessionLogName.MatchString(name) }

// RotateLogs scans the logDir and removes old logs based on age and total
// session log size. Logs of sessions that are still running are never removed;
// they still count towards the total size.
//
// Only logs that are about to be removed are opened: rotation runs at the end
// of every session, so it must not read every saved log each time.
func RotateLogs(logDir string, maxAgeDays int, maxTotalSizeMB int) error {
	entries, err := ListLogFiles(logDir)
	if err != nil {
		return err
	}

	type fileInfo struct {
		path    string
		size    int64
		modTime time.Time
	}

	var files []fileInfo
	var rotationErr error
	now := time.Now()
	maxAge := time.Duration(maxAgeDays) * 24 * time.Hour

	// Name matches count provisionally. When a file comes up for removal,
	// drop its size if it is unidentifiable or gone. Identified active logs
	// and logs whose removal failed still count against the budget.
	exclude := func(path string, modTime time.Time) bool {
		drop, err := removeRotationCandidate(path, now.Sub(modTime))
		if err != nil {
			rotationErr = errors.Join(rotationErr, err)
		}
		return drop
	}

	for _, entry := range entries {
		if !IsSessionLogName(filepath.Base(entry.Path)) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if !os.IsNotExist(err) {
				rotationErr = errors.Join(rotationErr, fmt.Errorf("stat %s: %w", entry.RelativePath, err))
			}
			continue
		}

		// 1. Remove files older than maxAgeDays
		if maxAgeDays > 0 && now.Sub(info.ModTime()) > maxAge && exclude(entry.Path, info.ModTime()) {
			continue
		}
		// Files that could not be removed stay in the size accounting.
		files = append(files, fileInfo{path: entry.Path, size: info.Size(), modTime: info.ModTime()})
	}

	if maxTotalSizeMB <= 0 {
		return rotationErr
	}

	var totalSize int64
	for _, f := range files {
		totalSize += f.size
	}

	maxTotalSizeBytes := int64(maxTotalSizeMB) * 1024 * 1024
	if totalSize <= maxTotalSizeBytes {
		return rotationErr
	}

	// 2. Sort by modTime (oldest first) and delete until total size is within the limit
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	for _, f := range files {
		if totalSize <= maxTotalSizeBytes {
			break
		}
		if exclude(f.path, f.modTime) {
			totalSize -= f.size
		}
	}

	return rotationErr
}

// removeRotationCandidate returns whether to drop this file from the size total.
func removeRotationCandidate(path string, idle time.Duration) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return true, nil
	}
	defer f.Close()
	lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	identified, finished := RotationMetadata(f)
	if !identified {
		return true, nil
	}
	if lockErr != nil || (!finished && idle < abandonedLogAge) {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("remove %s: %w", path, err)
	}
	return true, nil
}

package logger

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

// createLogFile reserves a unique name atomically. A separate Stat followed
// by a truncating OpenFile would let simultaneous sessions overwrite each
// other, and would retry forever on stat errors other than ENOENT.
func createLogFile(dir, filename string) (*os.File, error) {
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	for i := 0; ; i++ {
		candidate := filename
		if i > 0 {
			candidate = fmt.Sprintf("%s_%d%s", name, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err == nil {
			// Held until Logger.Close; migration and rotation skip open sessions.
			if lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr != nil {
				name := f.Name()
				f.Close()
				_ = os.Remove(name)
				return nil, lockErr
			}
		}
		return f, err
	}
}

// CompressFile publishes a complete gzip file before deleting the source.
// Existing archives are never overwritten; on failure the source is kept
// and any temporary output is removed.
func CompressFile(srcPath string) (string, error) {
	dstPath := srcPath + ".gz"

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer srcFile.Close()

	dstFile, err := os.CreateTemp(filepath.Dir(srcPath), ".kuroko-compress-*")
	if err != nil {
		return "", err
	}
	defer dstFile.Close()
	defer os.Remove(dstFile.Name())

	if err := compressStream(dstFile, srcFile); err != nil {
		return "", err
	}

	if err := dstFile.Sync(); err != nil {
		return "", err
	}
	if err := dstFile.Close(); err != nil {
		return "", err
	}
	if err := srcFile.Close(); err != nil {
		return "", err
	}
	// Linking within the same directory publishes the finished file in one
	// step and fails if dstPath already exists, including a dangling symlink.
	if err := os.Link(dstFile.Name(), dstPath); err != nil {
		return "", err
	}

	if err := os.Remove(srcPath); err != nil {
		return "", err
	}

	return dstPath, nil
}

func compressStream(dst io.Writer, src io.Reader) error {
	zw := gzip.NewWriter(dst)
	_, copyErr := io.Copy(zw, src)
	// gzip buffers data: a successful copy does not imply a successful
	// archive until Close has written the compressed tail and checksum.
	return errors.Join(copyErr, zw.Close())
}

// abandonedLogAge is how long a log without a completion footer must have
// been untouched before retention cleanup treats it as abandoned (its session
// was killed or crashed) rather than as a session still running under an
// older kuroko that did not lock its log.
const abandonedLogAge = 24 * time.Hour

// RotateLogs scans the logDir and removes old logs based on age and total
// directory size. Logs of sessions that are still running are never removed;
// they still count towards the total size.
//
// Only logs that are about to be removed are opened: rotation runs at the end
// of every session, so it must not read every saved log each time.
func RotateLogs(logDir string, maxAgeDays int, maxTotalSizeMB int) error {
	entries, err := logstore.ListLogFiles(logDir)
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

	// remove deletes path unless its session is still running, and reports
	// whether the file is gone.
	remove := func(path string, modTime time.Time) bool {
		if activeLog(path, now.Sub(modTime)) {
			return false
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			rotationErr = errors.Join(rotationErr, fmt.Errorf("remove %s: %w", path, err))
			return false
		}
		return true
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			if !os.IsNotExist(err) {
				rotationErr = errors.Join(rotationErr, fmt.Errorf("stat %s: %w", entry.RelativePath, err))
			}
			continue
		}

		// 1. Remove files older than maxAgeDays
		if maxAgeDays > 0 && now.Sub(info.ModTime()) > maxAge && remove(entry.Path, info.ModTime()) {
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
		if remove(f.path, f.modTime) {
			totalSize -= f.size
		}
	}

	return rotationErr
}

// activeLog reports whether the log at path may belong to a session that is
// still running; idle is how long ago the file was last written.
//
// A running session holds an advisory lock on its log. Logs from older kuroko
// versions have no lock, so a recognized session log without a completion
// footer is also left alone — but only while it is recent: once it has been
// idle for abandonedLogAge, its session was killed or crashed, and keeping it
// forever would put it beyond retention cleanup.
func activeLog(path string, idle time.Duration) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	// A compressed log was closed before it was compressed, and reading its
	// footer would mean decompressing all of it.
	if strings.HasSuffix(path, ".gz") || idle >= abandonedLogAge {
		return false
	}
	s, err := logstore.ReadSummary(path)
	return err != nil || (s.Command != "" && s.ExitCode == nil)
}

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
	"time"

	"github.com/ryu/kuroko/internal/logstore"
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

// RotateLogs scans the logDir and removes old logs based on age and total directory size.
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

	for _, entry := range entries {
		name := entry.Name()

		info, err := entry.Info()
		if err != nil {
			if !os.IsNotExist(err) {
				rotationErr = errors.Join(rotationErr, fmt.Errorf("stat %s: %w", name, err))
			}
			continue
		}

		path := filepath.Join(logDir, name)
		modTime := info.ModTime()

		// 1. Remove files older than maxAgeDays
		if maxAgeDays > 0 && now.Sub(modTime) > maxAge {
			if err := os.Remove(path); err == nil || os.IsNotExist(err) {
				continue
			} else {
				rotationErr = errors.Join(rotationErr, fmt.Errorf("remove %s: %w", path, err))
			}
			// Keep failed deletions in the size accounting.
		}

		files = append(files, fileInfo{
			path:    path,
			size:    info.Size(),
			modTime: modTime,
		})
	}

	if maxTotalSizeMB <= 0 {
		return rotationErr
	}

	// Calculate total size and check if it exceeds the limit
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
		if err := os.Remove(f.path); err == nil || os.IsNotExist(err) {
			totalSize -= f.size
		} else {
			rotationErr = errors.Join(rotationErr, fmt.Errorf("remove %s: %w", f.path, err))
		}
	}

	return rotationErr
}

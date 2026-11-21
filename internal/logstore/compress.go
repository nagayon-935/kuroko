package logstore

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

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

	if err := compressStreamWithComment(dstFile, srcFile, summaryComment(srcPath)); err != nil {
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

// summaryComment builds the gzip header comment that lets readers learn how
// a session ended without decompressing the archive. It returns "" when the
// log has no complete footer (or cannot be read); that never blocks
// compression.
func summaryComment(srcPath string) string {
	s, err := ReadSummary(srcPath)
	if err != nil || s.ExitCode == nil || s.Ended.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s%s%s%d", summaryCommentPrefix, s.Ended.Format(time.RFC3339Nano), summaryCommentExit, *s.ExitCode)
}

func compressStream(dst io.Writer, src io.Reader) error {
	return compressStreamWithComment(dst, src, "")
}

func compressStreamWithComment(dst io.Writer, src io.Reader, comment string) error {
	zw := gzip.NewWriter(dst)
	zw.Comment = comment
	_, copyErr := io.Copy(zw, src)
	// gzip buffers data: a successful copy does not imply a successful
	// archive until Close has written the compressed tail and checksum.
	return errors.Join(copyErr, zw.Close())
}

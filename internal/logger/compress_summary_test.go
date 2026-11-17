package logger

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gzipComment(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	return zr.Header.Comment
}

func TestCompressFileRecordsSummaryInGzipComment(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	src := filepath.Join(dir, "20260617_180000_ssh_sw1.log")
	content := "# kuroko session log\n# Started : 2026-06-17T18:00:00.000000000+09:00\n# Command : ssh sw1\n# Target  : sw1\n# ----\n\nbody\n\n# " + strings.Repeat("-", 68) + "\n# Ended   : 2026-06-17T19:30:00.123456789+09:00\n# Exit    : 0\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	dst, err := CompressFile(src)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	want := "kuroko-summary ended=2026-06-17T19:30:00.123456789+09:00 exit=0"
	if got := gzipComment(t, dst); got != want {
		t.Errorf("Comment = %q; want %q", got, want)
	}
}

func TestCompressFileRecordsNonZeroExit(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	src := filepath.Join(dir, "s.log")
	content := "# kuroko session log\n# Started : 2026-06-17T18:00:00Z\n# Command : bash\n# Target  : \n# ----\n\nx\n\n# " + strings.Repeat("-", 68) + "\n# Ended   : 2026-06-17T18:05:00Z\n# Exit    : 130\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	dst, err := CompressFile(src)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if got := gzipComment(t, dst); got != "kuroko-summary ended=2026-06-17T18:05:00Z exit=130" {
		t.Errorf("Comment = %q", got)
	}
}

func TestCompressFileWithoutFooterLeavesCommentEmpty(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	src := filepath.Join(dir, "crashed.log")
	if err := os.WriteFile(src, []byte("# kuroko session log\n# Started : 2026-06-17T18:00:00Z\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	dst, err := CompressFile(src)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if got := gzipComment(t, dst); got != "" {
		t.Errorf("Comment = %q; want empty", got)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should be removed, stat err = %v", err)
	}
}

package logstore

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompressFile(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "test.log")
	content := []byte("hello compression test\n")
	if err := os.WriteFile(srcPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	dstPath, err := CompressFile(srcPath)
	if err != nil {
		t.Fatalf("CompressFile error: %v", err)
	}

	// Verify original file is deleted
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Errorf("original file still exists: %s", srcPath)
	}

	// Verify destination file ends with .gz
	if !strings.HasSuffix(dstPath, ".gz") {
		t.Errorf("expected destination path to end with .gz, got: %s", dstPath)
	}

	// Read and decompress
	f, err := os.Open(dstPath)
	if err != nil {
		t.Fatalf("open compressed file error: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("new gzip reader error: %v", err)
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read decompressed error: %v", err)
	}

	if string(decompressed) != string(content) {
		t.Errorf("decompressed content = %q, want %q", string(decompressed), string(content))
	}
}

func TestCompressFilePreservesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "session.log")
	dst := src + ".gz"
	for path, content := range map[string]string{src: "new log", dst: "existing archive"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CompressFile(src); err == nil {
		t.Fatal("expected error for existing destination")
	}
	for path, want := range map[string]string{src: "new log", dst: "existing archive"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", path, data, err, want)
		}
	}
}

func TestCompressFileRemovesPartialOutputOnReadError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "session.log")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := CompressFile(src); err == nil {
		t.Fatal("expected error when reading a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "session.log" {
		t.Fatalf("compression left partial output: %v", entries)
	}
}

type failAfterHeaderWriter struct{ writes int }

func (w *failAfterHeaderWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestCompressStreamReportsFinalFlushError(t *testing.T) {
	w := &failAfterHeaderWriter{}
	if err := compressStream(w, strings.NewReader("small log")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("compressStream = %v; want final flush error", err)
	}
}

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

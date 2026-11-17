package logger

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

// The gzip header comment is written here and parsed in logstore; this pins
// the two sides to the same format.
func TestCompressFileSummaryIsReadableFromGzipHeader(t *testing.T) {
	// Arrange: a finished session log.
	l, err := New(t.TempDir(), []string{"ssh", "sw1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("hello\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(7); err != nil {
		t.Fatal(err)
	}
	want, err := logstore.ReadSummary(l.Path)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	gzPath, err := CompressFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: the header comment alone carries the footer data.
	f, err := os.Open(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(zr.Comment, "kuroko-summary ") {
		t.Fatalf("gzip comment = %q, want a kuroko-summary", zr.Comment)
	}
	got, err := logstore.ReadSummary(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitCode == nil || *got.ExitCode != 7 || !got.Ended.Equal(want.Ended) || got.Target != want.Target {
		t.Errorf("summary of %s = %+v, want %+v", filepath.Base(gzPath), got, want)
	}
}

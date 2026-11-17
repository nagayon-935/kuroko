package logsearch

import (
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeLog(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeGz(t *testing.T, dir, rel, content string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	writeLog(t, dir, rel, buf.String())
}

func searchResult(t *testing.T, dir, pattern string, opt Options) (res Result, out, warn string) {
	t.Helper()
	var o, w bytes.Buffer
	res, err := Search(dir, regexp.MustCompile(pattern), opt, &o, &w)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	return res, o.String(), w.String()
}

func search(t *testing.T, dir, pattern string, opt Options) (matched bool, out, warn string) {
	t.Helper()
	res, out, warn := searchResult(t, dir, pattern, opt)
	return res.Matched, out, warn
}

func TestSearchFindsMatchesInPlainAndGzipLogsInPathOrder(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "ssh/b/20260101_000000_ssh_b.log", "ok\nlink down\n")
	writeGz(t, dir, "ssh/a/20260101_000000_ssh_a.log.gz", "link down\nfine\n")

	matched, out, warn := search(t, dir, "down", Options{})

	want := "ssh/a/20260101_000000_ssh_a.log.gz:1:link down\nssh/b/20260101_000000_ssh_b.log:2:link down\n"
	if !matched || out != want || warn != "" {
		t.Fatalf("got matched=%v out=%q warn=%q; want out %q", matched, out, warn, want)
	}
}

func TestSearchCaseInsensitiveViaPattern(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log", "ERROR here\n")
	if m, _, _ := search(t, dir, "error", Options{}); m {
		t.Fatal("case-sensitive pattern matched different case")
	}
	if m, _, _ := search(t, dir, "(?i)error", Options{}); !m {
		t.Fatal("(?i) pattern did not match")
	}
}

func TestSearchFilesOnlyPrintsEachLogOnce(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log", "x\nx\nx\n")
	writeLog(t, dir, "b.log", "y\n")

	matched, out, _ := search(t, dir, "x", Options{FilesOnly: true})

	if !matched || out != "a.log\n" {
		t.Fatalf("got matched=%v out=%q; want only a.log once", matched, out)
	}
}

func TestSearchNoMatchReportsFalse(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log", "nothing\n")
	if m, out, _ := search(t, dir, "zzz", Options{}); m || out != "" {
		t.Fatalf("got matched=%v out=%q; want no match", m, out)
	}
}

func TestSearchMissingLogDirIsNoMatch(t *testing.T) {
	m, out, _ := search(t, filepath.Join(t.TempDir(), "absent"), "x", Options{})
	if m || out != "" {
		t.Fatalf("got matched=%v out=%q", m, out)
	}
}

func TestSearchSkipsMetadataLinesButCountsThem(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log", "# kuroko:cmd:2026-01-01T00:00:00Z\nshow version\n# kuroko:cmd:2026-01-01T00:00:01Z\nshow version\n")

	_, out, _ := search(t, dir, "kuroko|version", Options{})

	if out != "a.log:2:show version\na.log:4:show version\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestSearchContinuesPastUnreadableLog(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log.gz", "this is not gzip")
	writeLog(t, dir, "b.log", "hit\n")

	matched, out, warn := search(t, dir, "hit", Options{})

	if !matched || out != "b.log:1:hit\n" {
		t.Fatalf("matched=%v out=%q", matched, out)
	}
	if !strings.Contains(warn, "warning: skipping a.log.gz") {
		t.Fatalf("warn = %q; want a skip warning for a.log.gz", warn)
	}
}

func TestSearchHandlesOverlongLinesDeliberately(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("a", MaxLineBytes+5000)
	writeLog(t, dir, "a.log", "start\n"+long+"TAILMARK\nafter hit\n")

	_, out, _ := search(t, dir, "^start|^aaaa|after hit|TAILMARK", Options{})

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d output lines, want 3 (start, truncated long, after): %.80q", len(lines), out)
	}
	if lines[0] != "a.log:1:start" || lines[2] != "a.log:3:after hit" {
		t.Fatalf("lines around the long line wrong: %q / %q", lines[0], lines[2])
	}
	if strings.Contains(lines[1], "TAILMARK") || len(lines[1]) > MaxLineBytes+len("a.log:2:") {
		t.Fatalf("long line not truncated to MaxLineBytes (len %d)", len(lines[1]))
	}
}

func TestSearchHandlesCRLFAndUnterminatedLastLine(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log", "one\r\nlast hit")

	_, out, _ := search(t, dir, "one|hit", Options{})

	if out != "a.log:1:one\na.log:2:last hit\n" {
		t.Fatalf("out = %q", out)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestSearchCountsSkippedLogs(t *testing.T) {
	tests := []struct {
		name        string
		files       map[string]string
		wantSkipped int
	}{
		{"none unreadable", map[string]string{"a.log": "hit\n"}, 0},
		{"one corrupt gzip", map[string]string{"a.log.gz": "not gzip", "b.log": "hit\n"}, 1},
		{"two corrupt gzips", map[string]string{"a.log.gz": "bad", "b.log.gz": "worse"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tt.files {
				writeLog(t, dir, rel, content)
			}

			res, _, _ := searchResult(t, dir, "hit", Options{})

			if res.Skipped != tt.wantSkipped {
				t.Fatalf("Skipped = %d, want %d", res.Skipped, tt.wantSkipped)
			}
		})
	}
}

func TestSearchAbortsWhenOutputFails(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log", "hit\n")
	for _, opt := range []Options{{}, {FilesOnly: true}} {
		_, err := Search(dir, regexp.MustCompile("hit"), opt, failWriter{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "writing output") {
			t.Fatalf("Search(%+v) error = %v; want a writing output error", opt, err)
		}
	}
}

func TestSearchFailingWarningWriterIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log.gz", "not gzip")
	_, err := Search(dir, regexp.MustCompile("x"), Options{}, &bytes.Buffer{}, failWriter{})
	if err == nil {
		t.Fatal("Search() = nil; want error when warnings cannot be written")
	}
}

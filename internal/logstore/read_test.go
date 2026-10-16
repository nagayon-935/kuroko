package logstore

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sessionLog(start, end, target string, code int) string {
	return fmt.Sprintf("# kuroko session log\n# Started : %s\n# Command : ssh -p 2222 user@router\n# Target  : %s\n# %s\n\n%s\n# %s\n# Ended   : %s\n# Exit    : %d\n", start, target, strings.Repeat("-", 68), strings.Repeat("device output\n", 3000), strings.Repeat("-", 68), end, code)
}

func writeLog(t *testing.T, path, text string) {
	t.Helper()
	if !strings.HasSuffix(path, ".gz") {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	w := gzip.NewWriter(f)
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadSummaryPlainAndGzip(t *testing.T) {
	for _, ext := range []string{".log", ".log.gz"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session"+ext)
			writeLog(t, path, sessionLog("2026-10-08T10:00:00+09:00", "2026-10-08T10:02:03+09:00", "user@router", 7))
			s, err := ReadSummary(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.Target != "user@router" || s.Command != "ssh -p 2222 user@router" || s.Duration() != 123*time.Second || s.ExitCode == nil || *s.ExitCode != 7 {
				t.Fatalf("unexpected summary: %+v", s)
			}
		})
	}
}

func TestReadSummaryMissingFooterAndPlainText(t *testing.T) {
	for _, text := range []string{"plain output\n# Exit    : 5\n", "# kuroko session log\n# Started : 2026-10-08T10:00:00+09:00\n# Command : bash\n# ---\n\npartial output"} {
		path := filepath.Join(t.TempDir(), "session.log")
		writeLog(t, path, text)
		s, err := ReadSummary(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.ExitCode != nil || !s.Ended.IsZero() {
			t.Fatalf("invented completion metadata: %+v", s)
		}
	}
}

func TestReadSummaryInvalidGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log.gz")
	writeLog(t, path, sessionLog("2026-10-08T10:00:00+09:00", "2026-10-08T10:02:03+09:00", "router", 0))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSummary(path); err == nil {
		t.Fatal("expected gzip checksum error")
	}
}

func TestLatestLogFileUsesSessionStart(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "copied.log")
	latest := filepath.Join(dir, "new.log.gz")
	writeLog(t, old, sessionLog("2026-10-07T10:00:00+09:00", "2026-10-07T10:01:00+09:00", "old", 0))
	writeLog(t, latest, sessionLog("2026-10-08T10:00:00+09:00", "2026-10-08T10:01:00+09:00", "new", 0))
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.log.gz"), []byte("broken gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LatestLogFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != latest {
		t.Fatalf("latest=%s; want %s", got, latest)
	}
}

func TestLatestLogFileEmptyAndMissingDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{dir, filepath.Join(dir, "missing")} {
		if _, err := LatestLogFile(path); err == nil {
			t.Fatalf("expected no-logs error for %s", path)
		}
		if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
			t.Fatalf("read operation created a directory: %v", err)
		}
	}
}

func TestResolveViewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.log")
	writeLog(t, path, "plain output\n")
	lookup := func() (string, error) { return dir, nil }
	if got, err := ResolveViewFile([]string{"--last"}, lookup); err != nil || got != path {
		t.Fatalf("--last=(%s,%v)", got, err)
	}
	for _, args := range [][]string{nil, {"one", "two"}, {"--unknown"}} {
		if _, err := ResolveViewFile(args, lookup); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

func TestLatestLogFileSameSecondUsesModificationTime(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "session_9.log")
	newer := filepath.Join(dir, "session_10.log")
	text := sessionLog("2026-10-08T10:00:00+09:00", "2026-10-08T10:00:01+09:00", "router", 0)
	writeLog(t, old, text)
	writeLog(t, newer, text)
	when := time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, when, when); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, when.Add(time.Millisecond), when.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got, err := LatestLogFile(dir); err != nil || got != newer {
		t.Fatalf("latest=(%s,%v); want %s", got, err, newer)
	}
}

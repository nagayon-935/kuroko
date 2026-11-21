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

func TestSummaryDurationEdgeCases(t *testing.T) {
	base := time.Date(2026, 6, 17, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		s    Summary
		want time.Duration
	}{
		{"zero summary", Summary{}, 0},
		{"missing start", Summary{Ended: base}, 0},
		{"missing end", Summary{Started: base}, 0},
		{"end before start", Summary{Started: base, Ended: base.Add(-time.Second)}, 0},
		{"equal start and end", Summary{Started: base, Ended: base}, 0},
		{"normal span", Summary{Started: base, Ended: base.Add(90 * time.Second)}, 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Duration(); got != tt.want {
				t.Errorf("Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFooterOffsetEdgeCases(t *testing.T) {
	rule := strings.Repeat("-", 68)
	body := "output\n"
	footer := "\n# " + rule + "\n# Ended   : 2026-06-17T18:00:05Z\n# Exit    : 0\n"
	tests := []struct {
		name string
		data string
		want int // -1 means len(data)
	}{
		{"empty", "", -1},
		{"no footer", body, -1},
		{"complete footer", body + footer, len(body)},
		{"complete footer without trailing newline", body + strings.TrimSuffix(footer, "\n"), len(body)},
		{"crlf footer is not recognized", strings.ReplaceAll(body+footer, "\n", "\r\n"), -1},
		{"marker only", body + "\n# " + rule + "\n# Ended   : ", -1},
		{"missing exit line", body + "\n# " + rule + "\n# Ended   : 2026-06-17T18:00:05Z\n", -1},
		{"extra trailing line", body + footer + "trailing\n", -1},
		{"wrong exit label", body + "\n# " + rule + "\n# Ended   : 2026-06-17T18:00:05Z\n# Status  : 0\n", -1},
		{"invalid ended time", body + "\n# " + rule + "\n# Ended   : yesterday\n# Exit    : 0\n", -1},
		{"non numeric exit", body + "\n# " + rule + "\n# Ended   : 2026-06-17T18:00:05Z\n# Exit    : abc\n", -1},
		{"negative exit accepted", body + "\n# " + rule + "\n# Ended   : 2026-06-17T18:00:05Z\n# Exit    : -1\n", len(body)},
		{"last footer wins over earlier lookalike", body + footer + "more\n" + footer, len(body) + len(footer) + len("more\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			if want == -1 {
				want = len(tt.data)
			}
			if got := FooterOffset([]byte(tt.data)); got != want {
				t.Errorf("FooterOffset = %d, want %d", got, want)
			}
		})
	}
}

func TestTailWindowKeepsOnlyLastMetadataWindowBytes(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{"small write is appended", []string{"abc", "def"}, "abcdef"},
		{"single oversized write keeps tail", []string{strings.Repeat("x", metadataWindow) + "TAIL"}, strings.Repeat("x", metadataWindow-4) + "TAIL"},
		{"exact window write replaces content", []string{"old", strings.Repeat("y", metadataWindow)}, strings.Repeat("y", metadataWindow)},
		{"overflowing append drops oldest bytes", []string{strings.Repeat("a", metadataWindow-2), "bcde"}, strings.Repeat("a", metadataWindow-4) + "bcde"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &tailWindow{}
			for _, s := range tt.writes {
				n, err := w.Write([]byte(s))
				if err != nil || n != len(s) {
					t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(s))
				}
			}
			if string(w.data) != tt.want {
				t.Errorf("window = %d bytes %q..., want %d bytes", len(w.data), truncForLog(w.data), len(tt.want))
			}
			if len(w.data) > metadataWindow {
				t.Errorf("window exceeds metadataWindow: %d", len(w.data))
			}
		})
	}
}

func truncForLog(b []byte) string {
	if len(b) > 16 {
		return string(b[len(b)-16:])
	}
	return string(b)
}

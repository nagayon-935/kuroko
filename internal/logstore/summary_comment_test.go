package logstore

import (
	"bytes"
	"compress/gzip"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// randomBody returns incompressible-ish text so the gzip body is large.
func randomBody(n int) string {
	rng := rand.New(rand.NewSource(1))
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(strconv.FormatUint(rng.Uint64(), 36))
		b.WriteByte('\n')
	}
	return b.String()
}

func gzipBytes(t *testing.T, comment, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Comment = comment
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeBytes(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func logWithBody(start, end string, code int, body string) string {
	return "# kuroko session log\n# Started : " + start + "\n# Command : ssh router\n# Target  : router\n# " +
		strings.Repeat("-", 68) + "\n\n" + body + "\n# " + strings.Repeat("-", 68) + "\n# Ended   : " + end + "\n# Exit    : " + strconv.Itoa(code) + "\n"
}

func TestReadSummaryGzipCommentWinsOverFooter(t *testing.T) {
	// Arrange: the comment disagrees with the real footer.
	text := logWithBody("2026-06-17T19:00:00+09:00", "2026-06-17T19:01:00+09:00", 1, "x")
	path := writeBytes(t, "a.log.gz", gzipBytes(t, "kuroko-summary ended=2026-06-17T19:30:00.123456789+09:00 exit=42", text))

	// Act
	s, err := ReadSummary(path)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 17, 19, 30, 0, 123456789, time.FixedZone("", 9*3600))
	if !s.Ended.Equal(want) || s.ExitCode == nil || *s.ExitCode != 42 {
		t.Fatalf("comment not used: %+v", s)
	}
	if s.Command != "ssh router" || s.Target != "router" || s.Started.IsZero() {
		t.Fatalf("header fields lost: %+v", s)
	}
}

func TestReadSummaryGzipCommentDoesNotReadBody(t *testing.T) {
	// Arrange: truncate the compressed body so a full decompression fails.
	text := logWithBody("2026-06-17T19:00:00+09:00", "2026-06-17T19:01:00+09:00", 0, randomBody(1<<20))
	data := gzipBytes(t, "kuroko-summary ended=2026-06-17T19:01:00+09:00 exit=3", text)
	path := writeBytes(t, "a.log.gz", data[:len(data)/2])

	// Act
	s, err := ReadSummary(path)

	// Assert
	if err != nil {
		t.Fatalf("body was read: %v", err)
	}
	if s.ExitCode == nil || *s.ExitCode != 3 {
		t.Fatalf("unexpected summary: %+v", s)
	}

	// The same file without a comment must fail, proving the fast path is what avoided the read.
	data = gzipBytes(t, "", text)
	path = writeBytes(t, "b.log.gz", data[:len(data)/2])
	if _, err := ReadSummary(path); err == nil {
		t.Fatal("expected error reading truncated gzip without comment")
	}
}

func TestReadSummaryGzipWithoutCommentFallsBackToFooter(t *testing.T) {
	text := logWithBody("2026-06-17T19:00:00+09:00", "2026-06-17T19:01:00+09:00", 5, randomBody(50000))
	path := writeBytes(t, "a.log.gz", gzipBytes(t, "", text))

	s, err := ReadSummary(path)

	if err != nil {
		t.Fatal(err)
	}
	if s.ExitCode == nil || *s.ExitCode != 5 || s.Duration() != time.Minute {
		t.Fatalf("fallback failed: %+v", s)
	}
}

func TestReadSummaryGzipMalformedCommentFallsBack(t *testing.T) {
	tests := map[string]string{
		"wrong prefix":     "other ended=2026-06-17T19:30:00+09:00 exit=0",
		"missing exit":     "kuroko-summary ended=2026-06-17T19:30:00+09:00",
		"bad time":         "kuroko-summary ended=yesterday exit=0",
		"empty exit":       "kuroko-summary ended=2026-06-17T19:30:00+09:00 exit=",
		"non-numeric exit": "kuroko-summary ended=2026-06-17T19:30:00+09:00 exit=abc",
		"plus sign exit":   "kuroko-summary ended=2026-06-17T19:30:00+09:00 exit=+1",
		"trailing junk":    "kuroko-summary ended=2026-06-17T19:30:00+09:00 exit=1 extra",
		"leading space":    " kuroko-summary ended=2026-06-17T19:30:00+09:00 exit=1",
		"overflow exit":    "kuroko-summary ended=2026-06-17T19:30:00+09:00 exit=99999999999999999999999",
		"too long":         "kuroko-summary ended=2026-06-17T19:30:00+09:00 exit=1" + strings.Repeat(" ", 200),
	}
	text := logWithBody("2026-06-17T19:00:00+09:00", "2026-06-17T19:01:00+09:00", 9, "x")
	for name, comment := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeBytes(t, "a.log.gz", gzipBytes(t, comment, text))

			s, err := ReadSummary(path)

			if err != nil {
				t.Fatal(err)
			}
			if s.ExitCode == nil || *s.ExitCode != 9 {
				t.Fatalf("expected fallback to real footer, got %+v", s)
			}
		})
	}
}

func TestParseSummaryCommentNegativeExit(t *testing.T) {
	_, code, ok := parseSummaryComment("kuroko-summary ended=2026-06-17T19:30:00Z exit=-1")
	if !ok || code != -1 {
		t.Fatalf("got code=%d ok=%v", code, ok)
	}
}

func benchmarkReadSummary(b *testing.B, comment string) {
	text := logWithBody("2026-06-17T19:00:00+09:00", "2026-06-17T19:01:00+09:00", 0, randomBody(16<<20))
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Comment = comment
	if _, err := w.Write([]byte(text)); err != nil {
		b.Fatal(err)
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(b.TempDir(), "big.log.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := ReadSummary(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadSummaryGzipWithComment(b *testing.B) {
	benchmarkReadSummary(b, "kuroko-summary ended=2026-06-17T19:01:00+09:00 exit=0")
}

func BenchmarkReadSummaryGzipWithoutComment(b *testing.B) { benchmarkReadSummary(b, "") }

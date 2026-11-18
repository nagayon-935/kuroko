package follow

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	header = "# kuroko session log\n# Command : sh\n# ----\n"
	footer = "\n# " + "--------------------------------------------------------------------" + "\n# Ended   : 2026-06-17T18:00:00+09:00\n# Exit    : 0\n"
)

// syncBuffer is a bytes.Buffer safe for use while Follow writes in another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Error(err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Error(err)
	}
}

func startFollow(ctx context.Context, path string, out *syncBuffer) <-chan error {
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, out, Options{Interval: 5 * time.Millisecond}) }()
	return done
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Follow did not return in time")
		return nil
	}
}

func waitFor(t *testing.T, out *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("output %q never contained %q", out.String(), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestFollowFinishedLogPrintsFullyAndReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	content := header + "hello\n" + footer
	writeFile(t, path, content)
	var out syncBuffer

	done := startFollow(context.Background(), path, &out)

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() = %v", err)
	}
	if out.String() != content {
		t.Fatalf("output = %q; want %q", out.String(), content)
	}
}

func TestFollowStreamsAppendedDataUntilFooter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, header+"first\n")
	var out syncBuffer
	done := startFollow(context.Background(), path, &out)

	waitFor(t, &out, "first")
	appendFile(t, path, "partial-no-newline")
	waitFor(t, &out, "partial-no-newline")
	appendFile(t, path, " rest\n")
	appendFile(t, path, footer)

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() = %v", err)
	}
	want := header + "first\npartial-no-newline rest\n" + footer
	if out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}
}

func TestFollowStopsOnContextCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, header+"running\n")
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := startFollow(ctx, path, &out)
	waitFor(t, &out, "running")

	cancel()

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() after cancel = %v; want nil", err)
	}
}

func TestFollowFinishesCleanlyWhenFileIsRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, header+"data\n")
	var out syncBuffer
	done := startFollow(context.Background(), path, &out)
	waitFor(t, &out, "data")

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() after removal = %v; want nil", err)
	}
}

func writeGzipLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.log.gz")
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, raw.String())
	return path
}

func TestFollowGzipLogIsCopiedOnce(t *testing.T) {
	content := header + "zipped\n"
	path := writeGzipLog(t, content)
	var out syncBuffer

	err := Follow(context.Background(), path, &out, Options{})

	if err != nil || out.String() != content {
		t.Fatalf("Follow(gz) = %v, %q; want nil, %q", err, out.String(), content)
	}
}

func TestFollowGzipCancellation(t *testing.T) {
	content := header + strings.Repeat("0123456789abcdef\n", 256*1024)
	path := writeGzipLog(t, content)
	for _, tt := range []struct {
		name         string
		cancelBefore bool
		wantMinBytes int
		wantMaxBytes int
	}{
		{"already cancelled", true, 0, 0},
		{"cancel after first write", false, 1, 32 * 1024},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &cancelWriter{cancel: cancel}
			if tt.cancelBefore {
				cancel()
			}

			err := Follow(ctx, path, w, Options{})

			if err != nil {
				t.Fatalf("Follow() = %v; want nil", err)
			}
			if w.n < tt.wantMinBytes || w.n > tt.wantMaxBytes {
				t.Fatalf("wrote %d of %d bytes; want %d..%d", w.n, len(content), tt.wantMinBytes, tt.wantMaxBytes)
			}
		})
	}
}

func TestFollowIgnoresFooterLikeTextInTheBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, header+"echoed:"+footer+"still running\n")
	var out syncBuffer
	done := startFollow(context.Background(), path, &out)
	waitFor(t, &out, "still running")

	select {
	case err := <-done:
		t.Fatalf("Follow returned early (%v) on footer-like body text", err)
	case <-time.After(50 * time.Millisecond):
	}
	appendFile(t, path, footer)
	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() = %v", err)
	}
}

func TestFollowErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		path string
	}{
		{"missing plain log", filepath.Join(dir, "missing.log")},
		{"missing gzip log", filepath.Join(dir, "missing.log.gz")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Follow(context.Background(), tt.path, &syncBuffer{}, Options{}); err == nil {
				t.Fatal("Follow() = nil; want an error")
			}
		})
	}
	t.Run("writer failure", func(t *testing.T) {
		path := filepath.Join(dir, "w.log")
		writeFile(t, path, header+"x\n")
		if err := Follow(context.Background(), path, failWriter{}, Options{}); err == nil {
			t.Fatal("Follow() = nil; want the writer error")
		}
	})
	t.Run("gzip copy failure", func(t *testing.T) {
		path := filepath.Join(dir, "w.log.gz")
		var raw bytes.Buffer
		zw := gzip.NewWriter(&raw)
		zw.Write([]byte("x"))
		zw.Close()
		writeFile(t, path, raw.String())
		if err := Follow(context.Background(), path, failWriter{}, Options{}); err == nil {
			t.Fatal("Follow() = nil; want the writer error")
		}
	})
}

func TestAppendTailKeepsOnlyTheLastWindow(t *testing.T) {
	big := bytes.Repeat([]byte("a"), tailWindow)
	got := appendTail(append([]byte(nil), big...), []byte("zz"))
	if len(got) != tailWindow || !bytes.HasSuffix(got, []byte("zz")) {
		t.Fatalf("appendTail len=%d suffix=%q", len(got), got[len(got)-2:])
	}
}

func TestFollowWaitsForFooterSplitAcrossWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, header+"body\n")
	var out syncBuffer
	done := startFollow(context.Background(), path, &out)
	waitFor(t, &out, "body")

	// Everything but the final newline: FooterOffset already accepts this, but
	// the exit code could still be missing digits.
	appendFile(t, path, footer[:len(footer)-1])
	waitFor(t, &out, "# Exit    : 0")
	select {
	case err := <-done:
		t.Fatalf("Follow returned (%v) before the footer was complete", err)
	case <-time.After(50 * time.Millisecond):
	}
	appendFile(t, path, "\n")

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() = %v", err)
	}
	if want := header + "body\n" + footer; out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}
}

func TestFollowOutputsBytesAppendedJustBeforeRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, header+"data\n")
	var out syncBuffer
	done := startFollow(context.Background(), path, &out)
	waitFor(t, &out, "data")

	appendFile(t, path, "late\n")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() = %v", err)
	}
	if !strings.HasSuffix(out.String(), "data\nlate\n") {
		t.Fatalf("output = %q; want it to end with the late append", out.String())
	}
}

func TestFollowReturnsWhenFileIsTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, header+"data\n")
	var out syncBuffer
	done := startFollow(context.Background(), path, &out)
	waitFor(t, &out, "data")

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() after truncate = %v; want nil", err)
	}
}

func TestFollowReturnsWhenPathIsReplacedByAnotherFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	writeFile(t, path, header+"old\n")
	var out syncBuffer
	done := startFollow(context.Background(), path, &out)
	waitFor(t, &out, "old")

	repl := filepath.Join(dir, "new.log")
	writeFile(t, repl, "a different, longer log that must not be followed\n")
	if err := os.Rename(repl, path); err != nil {
		t.Fatal(err)
	}

	if err := waitDone(t, done); err != nil {
		t.Fatalf("Follow() after replace = %v; want nil", err)
	}
	if strings.Contains(out.String(), "different") {
		t.Fatalf("output %q contains the replacement file", out.String())
	}
}

// cancelWriter cancels a context on its first write.
type cancelWriter struct {
	cancel func()
	n      int
}

func (c *cancelWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	c.cancel()
	return len(p), nil
}

func TestFollowHonoursCancellationWhileCatchingUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	content := header + strings.Repeat("0123456789abcdef\n", 64*1024) // ~1 MiB
	writeFile(t, path, content)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &cancelWriter{cancel: cancel}

	err := Follow(ctx, path, w, Options{Interval: 5 * time.Millisecond})

	if err != nil {
		t.Fatalf("Follow() = %v; want nil", err)
	}
	if w.n == 0 || w.n >= len(content) {
		t.Fatalf("wrote %d of %d bytes; want the drain to stop early", w.n, len(content))
	}
}

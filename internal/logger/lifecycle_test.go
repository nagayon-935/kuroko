package logger

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoggerConcurrentWrites(t *testing.T) {
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close(0) })
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for row := 0; row < 40; row++ {
				if _, err := fmt.Fprintf(l, "worker-%d-row-%d\n", worker, row); err != nil {
					t.Error(err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	for worker := 0; worker < 8; worker++ {
		for row := 0; row < 40; row++ {
			want := fmt.Sprintf("worker-%d-row-%d\n", worker, row)
			if strings.Count(string(data), want) != 1 {
				t.Errorf("expected exactly one %q", want)
			}
		}
	}
}

func TestLoggerWriteAfterClose(t *testing.T) {
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	if n, err := l.Write([]byte("late output\n")); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after Close = (%d, %v); want (0, ErrClosed)", n, err)
	}
}

func TestLoggerCloseReleasesRawFileOnFlushError(t *testing.T) {
	t.Setenv("KUROKO_RAW_DEBUG", "1")
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	raw := l.rawFile
	if raw == nil {
		t.Fatal("expected raw debug file")
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := l.Write([]byte("buffered line\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.file.Close(); err != nil {
		t.Fatal(err)
	}
	closeErr := l.Close(0)
	if !errors.Is(closeErr, os.ErrClosed) {
		t.Fatalf("Close = %v; want ErrClosed", closeErr)
	}
	if _, err := raw.WriteString("late debug"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("raw file remains open after failed Close: %v", err)
	}
	if err := l.Close(1); err != closeErr {
		t.Fatalf("repeated Close = %v; want original error %v", err, closeErr)
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

func TestCreateLogFileConcurrentReservations(t *testing.T) {
	dir := t.TempDir()
	start := make(chan struct{})
	paths := make(chan string, 16)
	var wg sync.WaitGroup
	for i := 0; i < cap(paths); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f, err := createLogFile(dir, "session.log")
			if err != nil {
				t.Error(err)
				return
			}
			paths <- f.Name()
			if _, err := f.WriteString(f.Name()); err != nil {
				t.Error(err)
			}
			if err := f.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(paths)
	seen := make(map[string]bool)
	for path := range paths {
		if seen[path] {
			t.Errorf("duplicate path %q", path)
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil || string(data) != path {
			t.Errorf("log was overwritten: data=%q, err=%v; want %q", data, err, path)
		}
	}
	if len(seen) != cap(paths) {
		t.Fatalf("reserved %d paths; want %d", len(seen), cap(paths))
	}
}

func TestCreateLogFileSkipsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing")
	if err := os.Symlink(target, filepath.Join(dir, "session.log")); err != nil {
		t.Fatal(err)
	}
	f, err := createLogFile(dir, "session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if filepath.Base(f.Name()) != "session_1.log" {
		t.Errorf("reserved %s; want session_1.log", f.Name())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("dangling symlink target was created: %v", err)
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

func TestLoggerConcurrentWriteAndClose(t *testing.T) {
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := l.Write([]byte("output\n"))
			if err != nil && !errors.Is(err, os.ErrClosed) {
				t.Error(err)
			}
			_ = l.InAltScreen()
			if err := l.Close(0); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "# Ended   :") != 1 {
		t.Fatalf("expected exactly one footer:\n%s", data)
	}
}

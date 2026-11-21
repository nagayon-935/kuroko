package logger

import (
	"errors"
	"fmt"
	"os"
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

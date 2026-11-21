package logger

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNewLoggerFilePermissions(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "user@host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer l.Close(0)

	info, err := os.Stat(l.Path)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file permissions = %04o, want 0600", perm)
	}
}

func TestLoggerWriteAndClose(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"echo", "hello"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := l.Write([]byte("hello world\n")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err := l.Close(0); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "hello world") {
		t.Errorf("log missing written content, got:\n%s", content)
	}
	if !strings.Contains(content, "Exit    : 0") {
		t.Errorf("log missing exit code footer, got:\n%s", content)
	}
}

// TestLoggerRawDebug verifies that KUROKO_RAW_DEBUG=1 creates a .raw sidecar file.
func TestLoggerRawDebug(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("KUROKO_RAW_DEBUG", "1")

	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	l.Write([]byte("hello\n"))
	l.Close(0)

	rawPath := l.Path + ".raw"
	if _, err := os.Stat(rawPath); errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw debug file %s should exist", rawPath)
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", rawPath, err)
	}
	if !strings.Contains(string(raw), "=== write 1") {
		t.Errorf("raw file should contain write entries, got:\n%s", string(raw))
	}
}

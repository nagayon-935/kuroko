package session_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/session"
)

func minimalConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{LogDir: t.TempDir()}
}

func TestSessionNew(t *testing.T) {
	s, err := session.New(minimalConfig(t), []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil Session")
	}
}

func TestSessionNewBadLogDir(t *testing.T) {
	// Use a non-existent sub-directory of an existing parent: os.Stat returns
	// ENOENT so uniquePath returns immediately, then os.OpenFile fails because
	// the parent directory does not exist.
	cfg := &config.Config{LogDir: filepath.Join(t.TempDir(), "no_such_dir")}
	_, err := session.New(cfg, []string{"echo"})
	if err == nil {
		t.Fatal("expected error for non-existent log dir, got nil")
	}
}

func TestSessionRunPlain(t *testing.T) {
	// When running under `go test`, os.Stdin is a pipe (not a TTY), so
	// runWithPTY() falls through to runPlain() immediately.
	s, err := session.New(minimalConfig(t), []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	code, err := s.Run()
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestSessionRunNonZeroExit(t *testing.T) {
	s, err := session.New(minimalConfig(t), []string{"false"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	code, _ := s.Run()
	if code == 0 {
		t.Error("expected non-zero exit code from 'false'")
	}
}

func TestSessionRunCreatesLogFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{LogDir: tmp}

	s, err := session.New(cfg, []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := s.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one log file to be created")
	}
}

func TestSessionRunWithCompression(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		LogDir: tmp,
		Storage: config.StorageConfig{
			CompressOnClose:     true,
			CompressThresholdMB: 0, // 0 = always compress
		},
	}
	s, err := session.New(cfg, []string{"echo", "compress_test"})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	code, err := s.Run()
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var gzFound bool
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".gz") {
			gzFound = true
			break
		}
	}
	if !gzFound {
		t.Error("expected .gz log file after CompressOnClose")
	}
}

func TestSessionRunWithCompressionThreshold(t *testing.T) {
	// File is tiny (< 1 byte for threshold); shouldCompress must be false.
	tmp := t.TempDir()
	cfg := &config.Config{
		LogDir: tmp,
		Storage: config.StorageConfig{
			CompressOnClose:     true,
			CompressThresholdMB: 100, // 100 MB — our echo output is far below this
		},
	}
	s, err := session.New(cfg, []string{"echo", "small"})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if _, err := s.Run(); err != nil {
		t.Fatalf("Run(): %v", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".gz") {
			t.Errorf("unexpected .gz file %q; file was below threshold", e.Name())
		}
	}
}

func TestSessionRunWithRotation(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		LogDir: tmp,
		Storage: config.StorageConfig{
			Rotation: config.RotationConfig{
				Enabled:        true,
				MaxAgeDays:     30,
				MaxTotalSizeMB: 1024,
			},
		},
	}
	s, err := session.New(cfg, []string{"echo", "rotation_test"})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	code, err := s.Run()
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	// Rotation runs in background; just verify Run completes normally.
}

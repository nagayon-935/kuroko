package session

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/kuroko/internal/config"
)

func TestWriteCompletionQuotesActualPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router's log.log.gz")
	if err := os.WriteFile(path, []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	writeCompletion(&b, path, 7, 123*time.Second, nil, false)
	if !strings.Contains(b.String(), "saved → "+path) || !strings.Contains(b.String(), "exit 7, 2m3s") {
		t.Fatal(b.String())
	}
	quote := strings.TrimSpace(strings.Split(b.String(), "[kuroko] view: kuroko view ")[1])
	out, err := exec.Command("sh", "-c", "printf '%s' "+quote).Output()
	if err != nil || string(out) != path {
		t.Fatalf("view argument=%q,%v; want %q", out, err, path)
	}
}

func TestWriteCompletionDoesNotClaimFailedSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.log")
	if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		err    error
		failed bool
	}{{errors.New("disk full"), false}, {nil, true}} {
		var b bytes.Buffer
		writeCompletion(&b, path, 0, time.Second, tt.err, tt.failed)
		if !strings.Contains(b.String(), "may be incomplete") || strings.Contains(b.String(), "saved →") || strings.Contains(b.String(), "view:") {
			t.Fatal(b.String())
		}
	}
}

func TestWriteCompletionHandlesRotationRemoval(t *testing.T) {
	var b bytes.Buffer
	writeCompletion(&b, filepath.Join(t.TempDir(), "removed.log"), 0, time.Second, nil, false)
	if !strings.Contains(b.String(), "no longer retained") || strings.Contains(b.String(), "view:") {
		t.Fatal(b.String())
	}
}

func TestLogWriterTracksFailureForCompletion(t *testing.T) {
	s, err := New(&config.Config{LogDir: t.TempDir()}, []string{"echo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.log.Close(0); err != nil {
		t.Fatal(err)
	}
	w := s.logWriter()
	if n, err := w.Write([]byte("output\n")); err != nil || n != 7 {
		t.Fatalf("terminal interrupted: %d,%v", n, err)
	}
	if !s.logFailed.Load() {
		t.Fatal("failed write not tracked")
	}
}

func TestFinishCompletionUsesCompressedPath(t *testing.T) {
	s, err := New(&config.Config{LogDir: t.TempDir(), Storage: config.StorageConfig{CompressOnClose: true}}, []string{"echo", "saved"})
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = original; _ = w.Close(); _ = r.Close(); _ = s.log.Close(0) })
	s.start = time.Now()
	s.finish(0)
	os.Stderr = original
	_ = w.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "saved → "+s.log.Path+".gz") || !strings.Contains(b.String(), shellQuote(s.log.Path+".gz")) {
		t.Fatal(b.String())
	}
}

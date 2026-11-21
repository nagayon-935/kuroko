package logstore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/kuroko/internal/logger"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

// writeFooterlessLog writes a session log that has a header but no
// completion footer and holds no lock — what a killed session, or one still
// running under an older kuroko that did not lock its log, leaves behind —
// and dates it idle ago.
func writeFooterlessLog(t *testing.T, root, name string, size int, idle time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, "ssh", "router")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	header := "# kuroko session log\n# Started : 2020-01-01T00:00:00Z\n# Command : ssh router\n# Target  : router\n# " + strings.Repeat("-", 68) + "\n\n"
	if err := os.WriteFile(path, []byte(header+strings.Repeat("x", size)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-idle)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRotateByAgeRemovesAbandonedLogWithoutFooter(t *testing.T) {
	root := t.TempDir()
	abandoned := writeFooterlessLog(t, root, "20200101_000000_ssh_router.log", 10, 40*24*time.Hour)

	if err := logstore.RotateLogs(root, 30, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Error("abandoned footer-less log was not rotated")
	}
}

func TestRotateBySizeTreatsFooterlessLogByIdleTime(t *testing.T) {
	tests := []struct {
		name        string
		idle        time.Duration
		wantRemoved bool
	}{
		{"written recently: may be a running legacy session", 2 * time.Hour, false},
		{"just under the threshold", logstore.AbandonedLogAge - time.Minute, false},
		{"idle past the threshold: abandoned", logstore.AbandonedLogAge + time.Hour, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange: one footer-less log alone exceeds the 1 MiB limit, so
			// size rotation must decide whether it may be removed.
			root := t.TempDir()
			path := writeFooterlessLog(t, root, "20200101_000000_ssh_router.log", 2<<20, tt.idle)

			// Act
			if err := logstore.RotateLogs(root, 0, 1); err != nil {
				t.Fatal(err)
			}

			// Assert
			_, err := os.Stat(path)
			if removed := os.IsNotExist(err); removed != tt.wantRemoved {
				t.Errorf("removed = %v; want %v", removed, tt.wantRemoved)
			}
		})
	}
}

func TestRemoveRotationCandidate(t *testing.T) {
	root := t.TempDir()
	footerless := writeFooterlessLog(t, root, "a.log", 10, 0)
	closed, err := logger.New(root, []string{"ssh", "closed"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(0); err != nil {
		t.Fatal(err)
	}
	locked, err := logger.New(root, []string{"ssh", "locked"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Close(0)

	tests := []struct {
		name string
		path string
		idle time.Duration
		want bool
	}{
		{"locked by a running session, however old", locked.Path, 365 * 24 * time.Hour, true},
		{"footer-less and recent", footerless, time.Hour, true},
		{"footer-less and idle past the threshold", footerless, logstore.AbandonedLogAge, false},
		{"closed with a footer", closed.Path, time.Hour, false},
		{"missing file", filepath.Join(root, "missing.log"), time.Hour, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drop, err := logstore.RemoveRotationCandidate(tt.path, tt.idle)
			if err != nil {
				t.Fatal(err)
			}
			if kept := !drop; kept != tt.want {
				t.Errorf("kept in size total = %v; want %v", kept, tt.want)
			}
		})
	}
}

func TestRotateBySizeSkipsActiveSession(t *testing.T) {
	root := t.TempDir()
	active, err := logger.New(root, []string{"ssh", "active-router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(0)
	if _, err := active.Write([]byte(strings.Repeat("x", 2<<20) + "\n")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(active.Path, old, old); err != nil {
		t.Fatal(err)
	}
	closed, err := logger.New(root, []string{"ssh", "closed-router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Write([]byte(strings.Repeat("y", 2<<20) + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(0); err != nil {
		t.Fatal(err)
	}

	if err := logstore.RotateLogs(root, 0, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(active.Path); err != nil {
		t.Error("active session log removed by size rotation")
	}
	if _, err := os.Stat(closed.Path); !os.IsNotExist(err) {
		t.Error("closed log not removed although the total exceeds the limit")
	}
}

func TestRotateNestedLogsLeavesActiveSessions(t *testing.T) {
	root := t.TempDir()
	closed, err := logger.New(root, []string{"ssh", "old-router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(0); err != nil {
		t.Fatal(err)
	}
	active, err := logger.New(root, []string{"ssh", "active-router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(0)
	old := time.Now().Add(-40 * 24 * time.Hour)
	for _, path := range []string{closed.Path, active.Path} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := logstore.RotateLogs(root, 30, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(closed.Path); !os.IsNotExist(err) {
		t.Fatal("closed nested log not rotated")
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatal("active log removed")
	}
}

package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogSubdir(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"ssh", "-p", "2222", "admin@edgeSW03"}, filepath.Join("ssh", "edgeSW03")},
		{[]string{"/usr/bin/ssh", "user@10.0.0.1"}, filepath.Join("ssh", "10.0.0.1")},
		{[]string{"screen", "/dev/ttyUSB0", "115200"}, filepath.Join("screen", "ttyUSB0")},
		{[]string{"/bin/bash"}, "bash"}, {[]string{"python", "script.py"}, "python"}, {nil, "unknown"},
	} {
		if got := LogSubdir(tt.args); got != tt.want {
			t.Errorf("LogSubdir(%v)=%s; want %s", tt.args, got, tt.want)
		}
	}
}

func TestLogSubdirSpecialTargetsAreDistinctAndContained(t *testing.T) {
	seen := map[string]bool{}
	for _, target := range []string{"../router", "..", ".", "a/b", "a_b", "a%2Fb", "a\\b", "[::1]", "ルータ", strings.Repeat("x", 1000)} {
		subdir := LogSubdir([]string{"ssh", target})
		parts := strings.Split(subdir, string(filepath.Separator))
		if len(parts) != 2 || parts[1] == "" || strings.HasPrefix(parts[1], ".") || len(parts[1]) > 255 {
			t.Fatalf("unsafe subdir %q", subdir)
		}
		if seen[subdir] {
			t.Fatalf("target collision for %q", target)
		}
		seen[subdir] = true
	}
}

func TestLoggerCreatesPrivateTargetDirectories(t *testing.T) {
	root := t.TempDir()
	l, err := New(root, []string{"ssh", "admin@edgeSW03"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close(0)
	if filepath.Dir(l.Path) != filepath.Join(root, "ssh", "edgeSW03") {
		t.Fatal(l.Path)
	}
	for _, dir := range []string{filepath.Join(root, "ssh"), filepath.Join(root, "ssh", "edgeSW03")} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("permissions for %s: %v", dir, err)
		}
	}
}

func TestLoggerRefusesSymlinkedTargetDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "ssh")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, []string{"ssh", "router"}, false); err == nil {
		t.Fatal("followed symlink outside storage root")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside root")
	}
}

func TestRotateNestedLogsLeavesActiveSessions(t *testing.T) {
	root := t.TempDir()
	closed, err := New(root, []string{"ssh", "old-router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(0); err != nil {
		t.Fatal(err)
	}
	active, err := New(root, []string{"ssh", "active-router"}, false)
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
	if err := RotateLogs(root, 30, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(closed.Path); !os.IsNotExist(err) {
		t.Fatal("closed nested log not rotated")
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatal("active log removed")
	}
}

package logstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSubdirCreatesPrivateNestedDirectories(t *testing.T) {
	tests := []struct {
		name     string
		relative string
		wantRel  string
	}{
		{"empty returns root", "", "."},
		{"dot returns root", ".", "."},
		{"single component", "ssh", "ssh"},
		{"nested components", "ssh/edge", filepath.Join("ssh", "edge")},
		{"unclean path is normalized", "ssh/./edge/", filepath.Join("ssh", "edge")},
		{"inner dotdot staying inside root", "a/../b", "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "root")

			got, err := EnsureSubdir(root, tt.relative)

			if err != nil {
				t.Fatalf("EnsureSubdir error: %v", err)
			}
			if want := filepath.Join(root, tt.wantRel); got != want {
				t.Errorf("path = %q, want %q", got, want)
			}
			info, err := os.Stat(got)
			if err != nil || !info.IsDir() {
				t.Fatalf("result is not a directory: %v", err)
			}
			if perm := info.Mode().Perm(); perm != 0o700 {
				t.Errorf("perm = %o, want 700", perm)
			}
		})
	}
}

func TestEnsureSubdirIsIdempotent(t *testing.T) {
	root := t.TempDir()
	first, err := EnsureSubdir(root, "ssh/edge")
	if err != nil {
		t.Fatal(err)
	}

	second, err := EnsureSubdir(root, "ssh/edge")

	if err != nil || second != first {
		t.Fatalf("second call = %q, %v; want %q, nil", second, err, first)
	}
}

func TestEnsureSubdirRejectsUnsafeNames(t *testing.T) {
	tests := []struct{ name, relative string }{
		{"absolute path", "/etc"},
		{"bare parent", ".."},
		{"parent prefix", "../escape"},
		{"cleaned escape", "a/../../escape"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "root")

			_, err := EnsureSubdir(root, tt.relative)

			if err == nil {
				t.Fatal("expected error")
			}
			if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
				t.Errorf("root must not be created for rejected input, stat err = %v", statErr)
			}
		})
	}
}

func TestEnsureSubdirRejectsNonDirectoryComponents(t *testing.T) {
	t.Run("component is a regular file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "ssh"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := EnsureSubdir(root, "ssh/edge")

		if err == nil {
			t.Fatal("expected error when a component is a regular file")
		}
	})
	t.Run("component is a symlink to a directory", func(t *testing.T) {
		root := t.TempDir()
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(root, "ssh")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}

		_, err := EnsureSubdir(root, "ssh")

		if err == nil {
			t.Fatal("expected symlinked component to be rejected")
		}
	})
	t.Run("root is a regular file", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := EnsureSubdir(root, "ssh")

		if err == nil {
			t.Fatal("expected error when root is a regular file")
		}
	})
}

func TestEnsureSubdirPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	_, err := EnsureSubdir(root, "ssh")

	if err == nil {
		t.Fatal("expected permission error creating a subdirectory in a read-only root")
	}
}

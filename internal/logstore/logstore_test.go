package logstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListLogFiles(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(dir string) error
		want    []string
		wantErr bool
	}{
		{
			name: "returns .log and .log.gz files, skips others and dirs",
			setup: func(dir string) error {
				files := []string{
					"20260617_180000_ssh_edgeSW03.log",
					"20260101_000000_bash.log.gz",
					"notes.txt",
				}
				for _, f := range files {
					if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
						return err
					}
				}
				return os.Mkdir(filepath.Join(dir, "subdir.log"), 0o700)
			},
			want: []string{"20260101_000000_bash.log.gz", "20260617_180000_ssh_edgeSW03.log"},
		},
		{
			name:  "empty directory returns no entries",
			setup: func(dir string) error { return nil },
			want:  nil,
		},
		{
			name:    "nonexistent directory returns error",
			setup:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.wantErr {
				dir = filepath.Join(dir, "does-not-exist")
			} else if tt.setup != nil {
				if err := tt.setup(dir); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}

			entries, err := ListLogFiles(dir)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListLogFiles() error = %v", err)
			}

			var got []string
			for _, e := range entries {
				got = append(got, e.Name())
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// chdir switches the working directory for the rest of the test.
// (t.Chdir needs Go 1.24; go.mod targets 1.22.)
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestResolveLogFile(t *testing.T) {
	const name = "20260617_180000_ssh_edgeSW03.log"

	tests := []struct {
		name       string
		arg        string
		setupCwd   func(t *testing.T, cwd string)
		logDirErr  error
		want       func(cwd, logDir string) string
		wantErr    bool
		wantLookup bool
	}{
		{
			name:       "bare name missing from cwd resolves into log dir",
			arg:        name,
			want:       func(_, logDir string) string { return filepath.Join(logDir, name) },
			wantLookup: true,
		},
		{
			name: "bare name present in cwd wins over log dir",
			arg:  name,
			setupCwd: func(t *testing.T, cwd string) {
				writeFile(t, filepath.Join(cwd, name))
			},
			want: func(string, string) string { return name },
		},
		{
			name: "dangling symlink in cwd keeps precedence",
			arg:  name,
			setupCwd: func(t *testing.T, cwd string) {
				if err := os.Symlink("does-not-exist", filepath.Join(cwd, name)); err != nil {
					t.Fatalf("Symlink: %v", err)
				}
			},
			want: func(string, string) string { return name },
		},
		{
			name: "self-referencing symlink (ELOOP) is not treated as missing",
			arg:  name,
			setupCwd: func(t *testing.T, cwd string) {
				if err := os.Symlink(name, filepath.Join(cwd, name)); err != nil {
					t.Fatalf("Symlink: %v", err)
				}
			},
			want: func(string, string) string { return name },
		},
		{
			name: "explicit ./ prefix is not redirected",
			arg:  "./" + name,
			want: func(string, string) string { return name },
		},
		{
			name: "path with .. component is not redirected",
			arg:  "sub/../" + name,
			want: func(string, string) string { return name },
		},
		{
			name:       "unknown bare name is returned unchanged",
			arg:        "missing.log",
			want:       func(string, string) string { return "missing.log" },
			wantLookup: true,
		},
		{
			name:       "log dir error is reported when fallback is needed",
			arg:        name,
			logDirErr:  errors.New("bad config"),
			wantErr:    true,
			wantLookup: true,
		},
		{
			name: "log dir is not consulted when cwd file exists",
			arg:  name,
			setupCwd: func(t *testing.T, cwd string) {
				writeFile(t, filepath.Join(cwd, name))
			},
			logDirErr: errors.New("bad config"),
			want:      func(string, string) string { return name },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logDir := t.TempDir()
			writeFile(t, filepath.Join(logDir, name))
			cwd := t.TempDir()
			if tt.setupCwd != nil {
				tt.setupCwd(t, cwd)
			}
			chdir(t, cwd)

			looked := false
			got, err := ResolveLogFile(tt.arg, func() (string, error) {
				looked = true
				return logDir, tt.logDirErr
			})

			if looked != tt.wantLookup {
				t.Errorf("log dir lookup = %v, want %v", looked, tt.wantLookup)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveLogFile(%q) expected error, got %q", tt.arg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveLogFile(%q) error = %v", tt.arg, err)
			}
			if want := tt.want(cwd, logDir); got != want {
				t.Fatalf("ResolveLogFile(%q) = %q, want %q", tt.arg, got, want)
			}
		})
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

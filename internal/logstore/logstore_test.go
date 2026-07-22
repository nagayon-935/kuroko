package logstore

import (
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

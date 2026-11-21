package logstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotateLogs(t *testing.T) {
	const header = "# kuroko session log\n# Started : 2020-01-01T00:00:00Z\n# Command : ssh router\n# Target  : router\n# ----\n\n"
	footer := "\n# " + strings.Repeat("-", 68) + "\n# Ended   : 2020-01-01T00:01:00Z\n# Exit    : 0\n"
	session := header + "output" + footer
	const comment = "kuroko-summary ended=2020-01-01T00:01:00Z exit=0"
	type file struct {
		name, content, comment string
		ageDays                int
		removed                bool
	}
	large := strings.Repeat("x", 3<<19)
	for _, tt := range []struct {
		name            string
		maxAge, maxSize int
		files           []file
	}{
		{"age in a shared directory", 7, 0, []file{
			{"20200101_000000_ssh_router.log", session, "", 10, true},
			{"app.log", "foreign", "", 10, false},
			{"20200101_000000_other.log", "foreign" + footer, "", 10, false},
			{"20200101_000000_bad name.log", session, "", 10, false},
			{"20200101_000001_ssh_router.log", session, "", 5, false},
			{"20200101_000002_ssh_ルータ_1.log.gz", session, comment, 10, true},
			{"20200101_000003_shell.log.gz", session, "", 10, false},
			{"app.log.gz", "foreign", comment, 10, false},
		}},
		{"foreign files excluded from size", 0, 1, []file{
			{"app.log", large, "", 1, false},
			{"20200101_000000_other.log", large, "", 10, false},
			{"20200101_000001_shell.log", session, "", 5, false},
		}},
		{"size removes oldest sessions first", 0, 1, []file{
			{"20200101_000000_other.log", large, "", 10, false},
			{"20200101_000001_shell.log", session, "", 5, true},
			{"20200101_000002_shell.log", header + large + footer, "", 2, true},
			{"20200101_000003_shell.log.gz", session, comment, 1, false},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				path := filepath.Join(dir, f.name)
				data := []byte(f.content)
				if strings.HasSuffix(f.name, ".gz") {
					var archive bytes.Buffer
					if err := compressStreamWithComment(&archive, strings.NewReader(f.content), f.comment); err != nil {
						t.Fatal(err)
					}
					data = archive.Bytes()
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				mtime := time.Now().Add(-time.Duration(f.ageDays) * 24 * time.Hour)
				if err := os.Chtimes(path, mtime, mtime); err != nil {
					t.Fatal(err)
				}
			}

			if err := RotateLogs(dir, tt.maxAge, tt.maxSize); err != nil {
				t.Fatal(err)
			}

			for _, f := range tt.files {
				_, err := os.Stat(filepath.Join(dir, f.name))
				if f.removed && !os.IsNotExist(err) || !f.removed && err != nil {
					t.Errorf("%s: want removed=%v, stat error=%v", f.name, f.removed, err)
				}
			}
		})
	}
}

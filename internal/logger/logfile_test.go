package logger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

// logstore.RotateLogs only removes files whose names pass
// IsSessionLogName, so every name the writer can produce must pass it.
func TestGeneratedFilenamesAreSessionLogNames(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"bash"},
		{"/bin/bash"},
		{"ssh", "edgeSW03"},
		{"ssh", "-p", "2222", "admin@edgeSW03"},
		{"ssh", "user@10.0.0.1"},
		{"screen", "/dev/ttyUSB0", "115200"},
		{"python", "script.py"},
		{"ssh", "ルータ"},
		{"ssh", "host with space"},
		{"ssh", "a:b*c?d"},
	} {
		name := generateFilename(args)
		if !logstore.IsSessionLogName(name) {
			t.Errorf("generateFilename(%v) = %q; not accepted by logstore.IsSessionLogName", args, name)
		}
	}
}

func TestCreatedLogFilesWithCollisionSuffixesAreSessionLogNames(t *testing.T) {
	dir := t.TempDir()
	const filename = "20260617_180000_ssh_edgeSW03.log"
	for i := 0; i < 12; i++ {
		f, err := createLogFile(dir, filename)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		base := filepath.Base(f.Name())
		if !logstore.IsSessionLogName(base) {
			t.Errorf("created %q; not accepted by logstore.IsSessionLogName", base)
		}
		if _, err := os.Stat(f.Name()); err != nil {
			t.Fatal(err)
		}
	}
}

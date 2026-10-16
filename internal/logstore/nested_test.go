package logstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedEnumerationResolutionAndInfo(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	chdir(t, cwd)
	files := map[string]string{"ssh/a/same.log": "aaa", "ssh/b/same.log": "bb", "bash/shell.log.gz": "not gzip, counted without reading content", "flat.log": "flat"}
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(cwd, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "flat.log"), filepath.Join(root, "alias.log")); err != nil {
		t.Fatal(err)
	}
	entries, err := ListLogFiles(root)
	if err != nil || len(entries) != 4 {
		t.Fatalf("files=%d,%v", len(entries), err)
	}
	resolve := func() (string, error) { return root, nil }
	if _, err := ResolveLogFile("same.log", resolve); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity: %v", err)
	}
	for _, arg := range []string{"ssh/a/same.log", "shell.log.gz"} {
		got, err := ResolveLogFile(filepath.FromSlash(arg), resolve)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(got); err != nil {
			t.Fatalf("relative lookup failed for %s: %v", arg, err)
		}
	}
	info, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	var wantBytes int64
	for _, s := range files {
		wantBytes += int64(len(s))
	}
	if info.Count != 4 || info.Bytes != wantBytes || len(info.Groups) != 4 {
		t.Fatalf("bad inventory: %+v", info)
	}
	var output strings.Builder
	if err := WriteInfo(&output, root); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{root, "Logs: 4", "ssh/a", "ssh/b", "bash", "unclassified"} {
		if !strings.Contains(output.String(), word) {
			t.Fatalf("info missing %q", word)
		}
	}
}

func TestInfoForMissingRootIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	info, err := Inspect(path)
	if err != nil || info.Count != 0 || info.Bytes != 0 {
		t.Fatalf("info=%+v,%v", info, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("info created a directory")
	}
}

func TestLatestIncludesNestedArchives(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ssh", "router", "new.log.gz")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeLog(t, path, sessionLog("2026-10-16T10:00:00+09:00", "2026-10-16T10:01:00+09:00", "router", 0))
	writeLog(t, filepath.Join(root, "old.log"), sessionLog("2026-10-15T10:00:00+09:00", "2026-10-15T10:01:00+09:00", "old", 0))
	if got, err := LatestLogFile(root); err != nil || got != path {
		t.Fatalf("latest=%s,%v", got, err)
	}
}

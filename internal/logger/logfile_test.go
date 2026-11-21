package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

func TestCreateLogFile(t *testing.T) {
	tmp := t.TempDir()

	create := func() string {
		t.Helper()
		f, err := createLogFile(tmp, "session.log")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return f.Name()
	}
	p1 := create()
	if !strings.HasSuffix(p1, "session.log") {
		t.Errorf("first path %q should end with session.log", p1)
	}

	p2 := create()
	if p2 == p1 {
		t.Errorf("second path should differ from first, got %q", p2)
	}
	if !strings.HasSuffix(p2, "session_1.log") {
		t.Errorf("second path %q should end with session_1.log", p2)
	}

	p3 := create()
	if !strings.HasSuffix(p3, "session_2.log") {
		t.Errorf("third path %q should end with session_2.log", p3)
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

func TestCreateLogFileConcurrentReservations(t *testing.T) {
	dir := t.TempDir()
	start := make(chan struct{})
	paths := make(chan string, 16)
	var wg sync.WaitGroup
	for i := 0; i < cap(paths); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f, err := createLogFile(dir, "session.log")
			if err != nil {
				t.Error(err)
				return
			}
			paths <- f.Name()
			if _, err := f.WriteString(f.Name()); err != nil {
				t.Error(err)
			}
			if err := f.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(paths)
	seen := make(map[string]bool)
	for path := range paths {
		if seen[path] {
			t.Errorf("duplicate path %q", path)
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil || string(data) != path {
			t.Errorf("log was overwritten: data=%q, err=%v; want %q", data, err, path)
		}
	}
	if len(seen) != cap(paths) {
		t.Fatalf("reserved %d paths; want %d", len(seen), cap(paths))
	}
}

func TestCreateLogFileSkipsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing")
	if err := os.Symlink(target, filepath.Join(dir, "session.log")); err != nil {
		t.Fatal(err)
	}
	f, err := createLogFile(dir, "session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if filepath.Base(f.Name()) != "session_1.log" {
		t.Errorf("reserved %s; want session_1.log", f.Name())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("dangling symlink target was created: %v", err)
	}
}

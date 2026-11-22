package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nagayon-935/kuroko/internal/logsearch"
)

// stubOpeners replaces the terminal check and the viewers --open launches,
// restoring them afterwards, and records what was launched.
type launched struct {
	lines   []string // "path:line" per openAtLine call
	pickers [][]logsearch.Match
	capped  []bool
}

func stubOpeners(t *testing.T, terminal bool) *launched {
	t.Helper()
	l := &launched{}
	origTerm, origLine, origPicker := interactiveTerminal, openAtLine, openPicker
	t.Cleanup(func() { interactiveTerminal, openAtLine, openPicker = origTerm, origLine, origPicker })
	interactiveTerminal = func() bool { return terminal }
	openAtLine = func(path string, line int) error {
		l.lines = append(l.lines, fmt.Sprintf("%s:%d", path, line))
		return nil
	}
	openPicker = func(ms []logsearch.Match, capped bool) error {
		l.pickers = append(l.pickers, ms)
		l.capped = append(l.capped, capped)
		return nil
	}
	return l
}

func TestGrepOpenFlagValidation(t *testing.T) {
	logDir := isolate(t)
	writeTestLog(t, logDir, "a.log", "hit\n")
	tests := []struct {
		name     string
		terminal bool
		args     []string
		wantErr  string
	}{
		{"open with -l", true, []string{"--open", "-l", "hit"}, "--open cannot be combined with -l"},
		{"-l with open", true, []string{"-l", "--open", "hit"}, "--open cannot be combined with -l"},
		{"not a terminal", false, []string{"--open", "hit"}, "--open needs an interactive terminal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := stubOpeners(t, tt.terminal)
			var out, errOut bytes.Buffer

			code, err := runGrep(tt.args, logDir, &out, &errOut)

			if code != 2 || err == nil || err.Error() != tt.wantErr {
				t.Fatalf("runGrep = %d, %v; want 2, %q", code, err, tt.wantErr)
			}
			if out.Len() != 0 || len(l.lines)+len(l.pickers) != 0 {
				t.Fatalf("something was printed or launched: %q %+v", out.String(), l)
			}
		})
	}
	// Through run(), the same failures are exit status 2.
	stubOpeners(t, false)
	if code, _, _ := runCLI(t, "-d", logDir, "grep", "--open", "hit"); code != 2 {
		t.Fatalf("run(grep --open, no terminal) = %d; want 2", code)
	}
}

func TestGrepOpenDispatch(t *testing.T) {
	clean := isolate(t)
	pathA := writeTestLog(t, clean, "ssh/a/20260101_000000_ssh_a.log", "# kuroko:cmd:2026-01-01T00:00:00Z\nInterface DOWN\nup\n")
	writeTestLog(t, clean, "ssh/b/20260101_000000_ssh_b.log", "DOWN\nDOWN again\n")

	t.Run("no match exits 1 without launching", func(t *testing.T) {
		l := stubOpeners(t, true)
		code, stdout, _ := runCLI(t, "-d", clean, "grep", "--open", "zzz-none")
		if code != 1 || stdout != "" || len(l.lines)+len(l.pickers) != 0 {
			t.Fatalf("code=%d stdout=%q launched=%+v", code, stdout, l)
		}
	})
	t.Run("one match opens the viewer at the line", func(t *testing.T) {
		l := stubOpeners(t, true)
		code, stdout, _ := runCLI(t, "-d", clean, "grep", "--open", "up$")
		if code != 0 || stdout != "" || !reflect.DeepEqual(l.lines, []string{pathA + ":3"}) || len(l.pickers) != 0 {
			t.Fatalf("code=%d stdout=%q launched=%+v; want 0, silent, %s:3", code, stdout, l, pathA)
		}
	})
	t.Run("several matches open the picker", func(t *testing.T) {
		l := stubOpeners(t, true)
		code, stdout, _ := runCLI(t, "-d", clean, "grep", "--open", "-i", "down")
		if code != 0 || stdout != "" || len(l.lines) != 0 || len(l.pickers) != 1 || len(l.pickers[0]) != 3 || l.capped[0] {
			t.Fatalf("code=%d stdout=%q launched=%+v; want the picker with 3 matches", code, stdout, l)
		}
		first := l.pickers[0][0]
		if first.RelativePath != filepath.ToSlash(filepath.Join("ssh", "a", "20260101_000000_ssh_a.log")) || first.Line != 2 || first.Path != pathA {
			t.Errorf("first match = %+v", first)
		}
	})
}

func TestGrepOpenSkippedLogIsExit2EvenWhenViewerOpened(t *testing.T) {
	logDir := isolate(t)
	writeTestLog(t, logDir, "a.log.gz", "not gzip")
	path := writeTestLog(t, logDir, "b.log", "hit\n")
	l := stubOpeners(t, true)

	code, _, stderr := runCLI(t, "-d", logDir, "grep", "--open", "hit")

	if code != 2 || !strings.Contains(stderr, "warning: skipping a.log.gz") || !reflect.DeepEqual(l.lines, []string{path + ":1"}) {
		t.Fatalf("code=%d stderr=%q launched=%+v", code, stderr, l)
	}
}

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunViewFollowStreamsFinishedLog(t *testing.T) {
	logDir := isolate(t)
	content := "# kuroko session log\nrouter output\n" + testFooter
	path := writeTestLog(t, logDir, "ssh/r1/20260617_180000_ssh_r1.log", content)

	for _, args := range [][]string{
		{"-d", logDir, "view", "--follow", "--last"},
		{"-d", logDir, "view", "--last", "-f"},
		{"-d", logDir, "view", "-f", path},
		{"-d", logDir, "view", path, "--follow"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		if code != 0 || stdout != content {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q; want 0 and the log", args, code, stdout, stderr)
		}
	}
}

func TestRunViewFollowReportsErrors(t *testing.T) {
	logDir := isolate(t)
	for _, args := range [][]string{
		{"-d", logDir, "view", "--follow"},                                  // no log argument
		{"-d", logDir, "view", "--follow", filepath.Join(logDir, "no.log")}, // missing file
		{"-d", logDir, "view", "--follow", "--bogus", "x.log"},              // unknown option still rejected
	} {
		code, _, stderr := runCLI(t, args...)
		if code != 1 {
			t.Errorf("run(%q) = %d, stderr %q; want 1", args, code, stderr)
		}
	}
}

func TestSplitFollowFlag(t *testing.T) {
	tests := []struct {
		name       string
		in, want   []string
		wantFollow bool
	}{
		{"long", []string{"--follow", "--last"}, []string{"--last"}, true},
		{"short after", []string{"a.log", "-f"}, []string{"a.log"}, true},
		{"absent", []string{"a.log"}, []string{"a.log"}, false},
		{"after delimiter", []string{"--", "-f"}, []string{"--", "-f"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, follow := splitFollowFlag(tt.in)
			if follow != tt.wantFollow || strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("splitFollowFlag(%q) = %q, %v; want %q, %v", tt.in, got, follow, tt.want, tt.wantFollow)
			}
		})
	}
}

func TestSplitLineFlag(t *testing.T) {
	tests := []struct {
		name      string
		in        []string
		wantRest  []string
		wantLine  int
		wantFound bool
		wantErr   bool
	}{
		{"absent", []string{"--last"}, []string{"--last"}, 0, false, false},
		{"separate value", []string{"--last", "--line", "42"}, []string{"--last"}, 42, true, false},
		{"equals value", []string{"--line=7", "x.log"}, []string{"x.log"}, 7, true, false},
		{"before and after the file", []string{"--line", "3", "x.log"}, []string{"x.log"}, 3, true, false},
		{"negative value is parsed", []string{"--line=-1", "x.log"}, []string{"x.log"}, -1, true, false},
		{"last one wins", []string{"--line=1", "--line", "2"}, nil, 2, true, false},
		{"after the delimiter it is a file name", []string{"--", "--line"}, []string{"--", "--line"}, 0, false, false},
		{"missing value", []string{"--line"}, nil, 0, false, true},
		{"not a number", []string{"--line", "abc"}, nil, 0, false, true},
		{"empty equals value", []string{"--line="}, nil, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rest, line, found, err := splitLineFlag(tt.in)
			if (err != nil) != tt.wantErr || line != tt.wantLine || found != tt.wantFound || strings.Join(rest, "|") != strings.Join(tt.wantRest, "|") {
				t.Fatalf("splitLineFlag(%q) = %q, %d, %v, %v", tt.in, rest, line, found, err)
			}
		})
	}
}

func TestViewLine(t *testing.T) {
	logDir := isolate(t)
	path := writeTestLog(t, logDir, "ssh/a/20260101_000000_ssh_a.log", "one\ntwo\nthree\n")

	t.Run("opens the viewer at the line", func(t *testing.T) {
		for _, args := range [][]string{
			{"view", "--line", "2", path},
			{"view", "--line=2", path},
			{"view", path, "--line", "2"}, // anywhere in view's arguments
			{"view", "--last", "--line=2"},
		} {
			l := stubOpeners(t, true)
			if code, _, _ := runCLI(t, append([]string{"-d", logDir}, args...)...); code != 0 || !reflect.DeepEqual(l.lines, []string{path + ":2"}) {
				t.Errorf("run(%q) = %d, launched %+v; want 0, %s:2", args, code, l, path)
			}
		}
	})
	t.Run("rejects --follow", func(t *testing.T) {
		l := stubOpeners(t, true)
		for _, args := range [][]string{{"view", "--follow", "--line", "1", path}, {"view", "-f", "--line=1", "--last"}} {
			if code, stdout, _ := runCLI(t, append([]string{"-d", logDir}, args...)...); code != 1 || stdout != "" {
				t.Errorf("run(%q) = %d, %q; want a usage error (1) and no output", args, code, stdout)
			}
		}
		if len(l.lines) != 0 {
			t.Fatalf("launched %+v", l)
		}
	})
	t.Run("bad values are errors", func(t *testing.T) {
		l := stubOpeners(t, true)
		for _, args := range [][]string{{"view", "--line", "x", path}, {"view", path, "--line"}} {
			if code, _, _ := runCLI(t, append([]string{"-d", logDir}, args...)...); code != 1 {
				t.Errorf("run(%q) = %d; want 1", args, code)
			}
		}
		if len(l.lines) != 0 {
			t.Fatalf("launched %+v", l)
		}
	})
	t.Run("out of range fails before the TUI", func(t *testing.T) {
		// The real viewer is used: it must reject the line without touching
		// the terminal.
		for _, line := range []string{"0", "4", "-2"} {
			if code, _, _ := runCLI(t, "-d", logDir, "view", "--line", line, path); code != 1 {
				t.Errorf("--line %s = %d; want 1", line, code)
			}
		}
	})
}

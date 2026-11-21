package viewer

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nagayon-935/kuroko/internal/logsearch"
)

const atLineFooter = "\n# --------------------------------------------------------------------\n# Ended   : 2026-10-08T10:00:03+09:00\n# Exit    : 0\n"

// atLineLog has 14 raw lines: header (1-3), a line before the first command
// (4), command 1 (metadata line 5, output 6-7), command 2 (metadata line 8,
// output 9-11, the last without LF) and a 3-line footer (12-14).
var atLineLines = []string{
	"# kuroko session log",
	"# Started : 2026-10-08T10:00:00+09:00",
	"# Command : ssh host",
	"banner before first command",
	"# kuroko:cmd:2026-10-08T10:00:01+09:00",
	"Router# show version",
	"IOS 15.2",
	"# kuroko:cmd:2026-10-08T10:00:02+09:00",
	"Router# show ip int brief",
	"Gi0/1 up",
	"Gi0/2 down",
}

func atLineText(_ string) string {
	return strings.Join(atLineLines, "\n") + atLineFooter
}

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

func TestLocateRawLine(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		text    string
		line    int
		wantCmd int
		wantRow int
		wantErr string
	}{
		{"header line", "a.log", atLineText(""), 1, 0, 0, ""},
		{"last header line", "a.log", atLineText(""), 3, 0, 2, ""},
		{"line before the first command", "a.log", atLineText(""), 4, 0, 3, ""},
		{"first metadata line", "a.log", atLineText(""), 5, 1, 0, ""},
		{"first output line of command 1", "a.log", atLineText(""), 6, 1, 0, ""},
		{"second output line of command 1", "a.log", atLineText(""), 7, 1, 1, ""},
		{"second metadata line", "a.log", atLineText(""), 8, 2, 0, ""},
		{"output line of command 2", "a.log", atLineText(""), 10, 2, 1, ""},
		{"last output line", "a.log", atLineText(""), 11, 2, 2, ""},
		{"footer separator", "a.log", atLineText(""), 12, 0, 9, ""},
		{"footer exit line", "a.log", atLineText(""), 14, 0, 11, ""},
		{"gz header", "a.log.gz", atLineText(""), 1, 0, 0, ""},
		{"gz output", "a.log.gz", atLineText(""), 10, 2, 1, ""},
		{"gz footer", "a.log.gz", atLineText(""), 13, 0, 10, ""},
		{"crlf header", "a.log", crlf(atLineText("")), 2, 0, 1, ""},
		{"crlf metadata line", "a.log", crlf(atLineText("")), 8, 2, 0, ""},
		{"crlf output", "a.log", crlf(atLineText("")), 7, 1, 1, ""},
		{"crlf last output", "a.log", crlf(atLineText("")), 11, 2, 2, ""},
		{"empty line ending the last command before the footer", "a.log",
			strings.Join(atLineLines, "\n") + "\n" + atLineFooter, 12, 2, 3, ""},
		{"no commands", "a.log", "alpha\nbeta\ngamma\n", 2, 0, 1, ""},
		{"no commands without final LF", "a.log", "alpha\nbeta\ngamma", 3, 0, 2, ""},
		{"zero is out of range", "a.log", atLineText(""), 0, 0, 0, "line 0 is out of range (the log has 14 lines)"},
		{"negative is out of range", "a.log", atLineText(""), -3, 0, 0, "line -3 is out of range (the log has 14 lines)"},
		{"past the end is out of range", "a.log", atLineText(""), 15, 0, 0, "line 15 is out of range (the log has 14 lines)"},
		{"no commands past the end", "a.log", "alpha\nbeta\ngamma\n", 4, 0, 0, "line 4 is out of range (the log has 3 lines)"},
		{"empty log", "a.log", "", 1, 0, 0, "line 1 is out of range (the log has 0 lines)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := saveViewerLog(t, t.TempDir(), tt.file, tt.text)
			v, err := newViewer(path)
			if err != nil {
				t.Fatal(err)
			}

			cmd, row, err := v.locateRawLine(tt.line)

			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("locateRawLine(%d) error = %v; want %q", tt.line, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cmd != tt.wantCmd || row != tt.wantRow {
				t.Fatalf("locateRawLine(%d) = (%d, %d); want (%d, %d)", tt.line, cmd, row, tt.wantCmd, tt.wantRow)
			}
		})
	}
}

// Every raw line that is not a metadata line must land on a display line with
// exactly the same bytes (the viewer does not trim CRs); a metadata line must
// land on the first line of its command.
func TestFocusRawLineShowsTheRawLine(t *testing.T) {
	for name, text := range map[string]string{
		"lf":            atLineText(""),
		"crlf":          crlf(atLineText("")),
		"empty-at-foot": strings.Join(atLineLines, "\n") + "\n" + atLineFooter,
		"no-commands":   "alpha\n\nbeta\r\ngamma",
	} {
		t.Run(name, func(t *testing.T) {
			path := saveViewerLog(t, t.TempDir(), "a.log", text)
			v, err := newViewer(path)
			if err != nil {
				t.Fatal(err)
			}
			raw := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
			for n, want := range raw {
				if err := v.focusRawLine(n + 1); err != nil {
					t.Fatalf("focusRawLine(%d): %v", n+1, err)
				}
				if v.highlight == 0 {
					t.Fatalf("line %d: nothing highlighted", n+1)
				}
				got := string(v.currentOutputLines[v.highlight-1])
				if strings.HasPrefix(want, "# kuroko:cmd:") {
					if v.highlight != 1 || v.activePane != PaneOutput {
						t.Errorf("metadata line %d: highlight = %d; want the first line of the command", n+1, v.highlight)
					}
					continue
				}
				if got != want {
					t.Errorf("line %d: display %q; want raw line %q", n+1, got, want)
				}
			}
		})
	}
}

func TestFocusRawLineSelectsCommandAndPane(t *testing.T) {
	path := saveViewerLog(t, t.TempDir(), "a.log", atLineText(""))
	v, err := newViewerAtLine(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.filteredIdx[v.selected]; got != 2 {
		t.Errorf("selected command = %d; want 2", got)
	}
	if v.activePane != PaneOutput || !v.revealHighlight || v.highlight != 2 {
		t.Errorf("pane=%v reveal=%v highlight=%d; want output pane, reveal pending, highlight 2", v.activePane, v.revealHighlight, v.highlight)
	}
	if got := string(v.currentOutputLines[v.highlight-1]); got != "Gi0/1 up" {
		t.Errorf("highlighted %q; want the raw line", got)
	}

	// Choosing another command drops the highlight.
	v.moveTimeline(-1)
	if v.highlight != 0 {
		t.Errorf("highlight = %d after selecting another command; want 0", v.highlight)
	}
}

func TestNewViewerAtLineOutOfRangeFailsBeforeTheTUI(t *testing.T) {
	path := saveViewerLog(t, t.TempDir(), "a.log", atLineText(""))
	if err := RunAtLine(path, 99); err == nil || err.Error() != "line 99 is out of range (the log has 14 lines)" {
		t.Fatalf("RunAtLine(99) = %v", err)
	}
	if err := RunAtLine(filepath.Join(t.TempDir(), "missing.log"), 1); err == nil {
		t.Fatal("RunAtLine(missing file) = nil")
	}
}

func TestDrawRevealsAndMarksTheLineOnceSizeIsKnown(t *testing.T) {
	var b strings.Builder
	b.WriteString("# kuroko:cmd:2026-10-08T10:00:01+09:00\nRouter# show log\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "entry %d\n", i)
	}
	path := saveViewerLog(t, t.TempDir(), "a.log", b.String())
	v, err := newViewerAtLine(path, 92) // "entry 90"
	if err != nil {
		t.Fatal(err)
	}
	v.outputQuery = "entry 9"
	v.updateMatches() // search matches coexist with the mark

	done := captureStdout(t)
	v.draw()
	out := done()

	if v.revealHighlight {
		t.Error("revealHighlight still set after the first draw")
	}
	if line := v.highlight - 1; line < v.outputScroll || line >= v.outputScroll+v.bodyHeight() {
		t.Errorf("highlight line %d outside the visible rows [%d, %d)", line, v.outputScroll, v.outputScroll+v.bodyHeight())
	}
	if !strings.Contains(out, "\x1b[1;37;44m") {
		t.Error("the highlighted line is not marked")
	}
	if !strings.Contains(stripANSI(out), "entry 90") {
		t.Error("the highlighted line is not drawn")
	}
}

// The viewer's mapping and `kuroko grep` must agree on every line grep
// reports, for plain, CRLF and gzip logs, including grep's CR trimming and
// its truncation of very long lines.
func TestRawLineMappingMatchesGrepOutput(t *testing.T) {
	long := "needle " + strings.Repeat("x", logsearch.MaxLineBytes+100)
	body := []string{
		"# kuroko session log",
		"# Command : ssh host",
		"needle in the header",
		"",
		"# kuroko:cmd:2026-10-08T10:00:01+09:00 needle",
		"Router# show needle",
		"needle with trailing text",
		"",
		long,
		"# kuroko:cmd:2026-10-08T10:00:02+09:00",
		"Router# show other",
		"another needle",
		"final needle",
	}
	plain := strings.Join(body, "\n") + atLineFooter
	dir := t.TempDir()
	saveViewerLog(t, dir, "plain.log", plain)
	saveViewerLog(t, dir, "crlf.log", crlf(plain))
	saveViewerLog(t, dir, "zipped.log.gz", plain)
	saveViewerLog(t, dir, "empty-line-foot.log", strings.Join(body, "\n")+"\n"+atLineFooter)
	saveViewerLog(t, dir, "nocmd.log", "needle\n\nplain\r\nneedle again")

	for _, pattern := range []string{"needle", "^$", "."} {
		var out, warn bytes.Buffer
		res, err := logsearch.Search(dir, regexp.MustCompile(pattern), logsearch.Options{}, &out, &warn)
		if err != nil || res.Skipped != 0 || !res.Matched {
			t.Fatalf("Search(%q) = %+v, %v, %q", pattern, res, err, warn.String())
		}
		viewers := map[string]*Viewer{}
		checked := 0
		for _, row := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
			parts := strings.SplitN(row, ":", 3)
			var n int
			if _, err := fmt.Sscanf(parts[1], "%d", &n); err != nil || len(parts) != 3 {
				t.Fatalf("unparseable grep row %.60q", row)
			}
			v := viewers[parts[0]]
			if v == nil {
				if v, err = newViewer(filepath.Join(dir, parts[0])); err != nil {
					t.Fatal(err)
				}
				viewers[parts[0]] = v
			}
			if err := v.focusRawLine(n); err != nil {
				t.Fatalf("%s:%d: %v", parts[0], n, err)
			}
			got := string(v.currentOutputLines[v.highlight-1])
			if len(got) > logsearch.MaxLineBytes {
				got = got[:logsearch.MaxLineBytes]
			}
			got = strings.TrimSuffix(got, "\r")
			if got != parts[2] {
				t.Errorf("%s:%d: viewer shows %.60q (%d bytes); grep printed %.60q (%d bytes)", parts[0], n, got, len(got), parts[2], len(parts[2]))
			}
			checked++
		}
		if checked == 0 {
			t.Fatalf("pattern %q matched nothing", pattern)
		}
	}
}

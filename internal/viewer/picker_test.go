package viewer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nagayon-935/kuroko/internal/logsearch"
	"github.com/nagayon-935/kuroko/internal/textwidth"
)

func newTestPicker(n int) *matchPicker {
	ms := make([]logsearch.Match, n)
	for i := range ms {
		ms[i] = logsearch.Match{Path: fmt.Sprintf("/logs/%d.log", i), RelativePath: fmt.Sprintf("ssh/%d.log", i), Line: i + 1, Text: fmt.Sprintf("hit %d", i)}
	}
	return &matchPicker{matches: ms, width: 40, height: 13} // body of 10 rows
}

func TestPickerKeys(t *testing.T) {
	esc := "\x1b"
	tests := []struct {
		name       string
		start      int
		key        string
		want       int
		wantAction pickerAction
	}{
		{"j moves down", 0, "j", 1, pickerNone},
		{"k moves up", 5, "k", 4, pickerNone},
		{"down arrow", 0, esc + "[B", 1, pickerNone},
		{"up arrow", 3, esc + "[A", 2, pickerNone},
		{"up stops at the first", 0, "k", 0, pickerNone},
		{"down stops at the last", 24, "j", 24, pickerNone},
		{"page down", 0, esc + "[6~", 10, pickerNone},
		{"page up", 15, esc + "[5~", 5, pickerNone},
		{"page down clamps", 20, esc + "[6~", 24, pickerNone},
		{"page up clamps", 3, esc + "[5~", 0, pickerNone},
		{"g jumps to the top", 12, "g", 0, pickerNone},
		{"G jumps to the bottom", 12, "G", 24, pickerNone},
		{"Home", 12, esc + "[H", 0, pickerNone},
		{"End", 12, esc + "[F", 24, pickerNone},
		{"Home as tilde key", 12, esc + "[1~", 0, pickerNone},
		{"End as tilde key", 12, esc + "[4~", 24, pickerNone},
		{"Enter opens", 7, "\r", 7, pickerOpen},
		{"LF opens", 7, "\n", 7, pickerOpen},
		{"q quits", 7, "q", 7, pickerQuit},
		{"Esc quits", 7, esc, 7, pickerQuit},
		{"Ctrl+C quits", 7, "\x03", 7, pickerQuit},
		{"unknown key is ignored", 7, "z", 7, pickerNone},
		{"mouse wheel down", 7, esc + "[<65;5;5M", 8, pickerNone},
		{"mouse wheel up", 7, esc + "[<64;5;5M", 6, pickerNone},
		{"click selects the row", 0, esc + "[<0;5;6M", 3, pickerNone},
		{"click below the rows is ignored", 2, esc + "[<0;5;40M", 2, pickerNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPicker(25)
			p.selected = tt.start

			action := p.handleInput([]byte(tt.key))

			if p.selected != tt.want || action != tt.wantAction {
				t.Fatalf("selected=%d action=%v; want %d, %v", p.selected, action, tt.want, tt.wantAction)
			}
		})
	}
}

func TestPickerRowsAndFooter(t *testing.T) {
	p := newTestPicker(25)
	p.selected = 2
	p.matches[2].Text = strings.Repeat("あ", 40) // wide runes must be cut by display width

	row := stripANSI(p.rowText(2))
	if !strings.HasPrefix(row, "> ssh/2.log:3:") || textwidth.String(row) > p.width || textwidth.String(row) < p.width-1 {
		t.Errorf("selected row = %q (width %d); want the marker, path:line:text and at most %d columns (a wide rune may leave one spare)", row, textwidth.String(row), p.width)
	}
	if row := p.rowText(0); !strings.HasPrefix(row, "  ssh/0.log:1:hit 0") {
		t.Errorf("unselected row = %q", row)
	}
	if got := p.footerText(); !strings.Contains(got, " 3/25 ") || !strings.Contains(got, "[Enter]: Open") || strings.Contains(got, "first") {
		t.Errorf("footer = %q", got)
	}
	p.capped = true
	if got := p.footerText(); !strings.Contains(got, "first 25 matches only") {
		t.Errorf("capped footer = %q", got)
	}
	p.statusMessage = "viewer error: boom"
	if got := p.footerText(); !strings.Contains(got, "boom") {
		t.Errorf("status footer = %q", got)
	}
}

func TestPickerDrawScrollsToTheSelection(t *testing.T) {
	p := newTestPicker(100)
	p.selected = 99

	done := captureStdout(t)
	p.draw()
	out := stripANSI(done())

	if !strings.Contains(out, "ssh/99.log:100:hit 99") || strings.Contains(out, "ssh/0.log:1:") {
		t.Errorf("draw did not scroll to the selection:\n%s", out)
	}
	if p.scroll == 0 {
		t.Error("scroll not updated")
	}
}

func TestRunPickerWithoutMatchesDoesNothing(t *testing.T) {
	if err := RunPicker(nil, false); err != nil {
		t.Fatalf("RunPicker(nil) = %v", err)
	}
}

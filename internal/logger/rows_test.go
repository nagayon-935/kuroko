package logger

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRowWindowCursorUpOverwritesPendingRow(t *testing.T) {
	var out bytes.Buffer
	w := rowWindow{out: &out}
	for _, text := range []string{"header", "spinner 1"} {
		if err := w.commit(pendingRow{text: []byte(text)}); err != nil {
			t.Fatal(err)
		}
	}
	w.up(1)
	if err := w.commit(pendingRow{text: []byte("spinner 2")}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("rows reached the output before they left the window: %q", out.String())
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "header\nspinner 2\n"; got != want {
		t.Fatalf("flushed rows = %q, want %q", got, want)
	}
}

func TestRowWindowWritesRowsThatScrollOutOfReach(t *testing.T) {
	var out bytes.Buffer
	w := rowWindow{out: &out}
	if err := w.commit(pendingRow{meta: []byte("# kuroko:cmd:t\n"), text: []byte("first")}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxPendingRows; i++ {
		if err := w.commit(pendingRow{text: []byte("row")}); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := out.String(), "# kuroko:cmd:t\nfirst\n"; got != want {
		t.Fatalf("output = %q, want only the oldest row %q", got, want)
	}
	if len(w.rows) != maxPendingRows {
		t.Fatalf("window holds %d rows, want %d", len(w.rows), maxPendingRows)
	}
}

func TestRowWindowSetLimitWritesRowsAboveTheScreen(t *testing.T) {
	var out bytes.Buffer
	w := rowWindow{out: &out}
	for _, text := range []string{"a", "b", "c", "d"} {
		if err := w.commit(pendingRow{text: []byte(text)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.setLimit(2); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "a\nb\n"; got != want {
		t.Fatalf("after shrinking to 2 rows, output = %q, want %q", got, want)
	}
	if err := w.commit(pendingRow{text: []byte("e")}); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "a\nb\nc\n"; got != want {
		t.Fatalf("after one more row, output = %q, want %q", got, want)
	}
	// A redraw within the screen still collapses in place.
	w.up(1)
	if err := w.commit(pendingRow{text: []byte("E")}); err != nil {
		t.Fatal(err)
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "a\nb\nc\nd\nE\n"; got != want {
		t.Fatalf("final output = %q, want %q", got, want)
	}
}

func TestLoggerSetScreenRowsLimitsUnwrittenOutput(t *testing.T) {
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	l.SetScreenRows(3)
	for i := 0; i < 10; i++ {
		if _, err := l.Write([]byte("line\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	// Without Close, as after a SIGKILL: only the rows still on screen may
	// be missing from the file.
	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte("line\n")); got != 7 {
		t.Fatalf("file holds %d of 10 lines before Close; want 7 (all but one screen)", got)
	}
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	l.SetScreenRows(1) // after Close: ignored
	data, err = os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte("line\n")); got != 10 {
		t.Fatalf("file holds %d of 10 lines after Close; want 10", got)
	}
}

// TestLoggerCursorUpRedrawCollapsed reproduces the live-redraw pattern used
// by Ink-style CLIs (e.g. Google's Antigravity CLI): a multi-line block is
// printed, then the cursor moves back up to the block's first line
// (ESC[<n>A) and reprints it with slightly different content, without ever
// entering the alternate screen. Before the fix, cursor-up was a no-op, so
// every redraw frame was permanently committed as new lines — a single
// animated box could balloon a session log to tens of thousands of
// duplicate lines. The logger must instead treat the redrawn block as an
// in-place overwrite of the still-pending (not yet flushed) rows.
func TestLoggerCursorUpRedrawCollapsed(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	frame := func(width int) string {
		return "line-a\nline-b\n" + strings.Repeat("-", width) + "\n"
	}

	// First frame: 3 lines.
	l.Write([]byte(frame(20)))
	// Redraw: move cursor up 3 lines and reprint with a narrower separator.
	l.Write([]byte("\x1b[3A"))
	l.Write([]byte(frame(10)))
	// Redraw again, narrower still — the final settled frame.
	l.Write([]byte("\x1b[3A"))
	l.Write([]byte(frame(5)))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	lines := strings.Split(content, "\n")

	if got := strings.Count(content, "line-a"); got != 1 {
		t.Errorf("expected exactly one settled 'line-a', got %d occurrences:\n%s", got, content)
	}

	wantLine := strings.Repeat("-", 5)
	staleLines := []string{strings.Repeat("-", 20), strings.Repeat("-", 10)}
	found := false
	for _, ln := range lines {
		if ln == wantLine {
			found = true
		}
		for _, stale := range staleLines {
			if ln == stale {
				t.Errorf("stale redraw frame line %q should not survive in log:\n%s", stale, content)
			}
		}
	}
	if !found {
		t.Errorf("expected final settled separator line %q in log:\n%s", wantLine, content)
	}
}

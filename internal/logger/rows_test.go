package logger

import (
	"bytes"
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

func TestLineBufferWideRuneOverwrite(t *testing.T) {
	var b lineBuffer
	for _, r := range "日本" {
		b.put(r)
	}
	b.backward(2)
	b.put('x')
	b.put('y')
	if got, want := string(b.text()), "日xy"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

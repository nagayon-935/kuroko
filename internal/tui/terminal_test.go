package tui

import (
	"os"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// withPTY points os.Stdin at a fresh PTY slave and discards os.Stdout for
// the duration of t.
func withPTY(t *testing.T) *os.File {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skip("pty.Open() unavailable:", err)
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("opening /dev/null:", err)
	}
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = tty, devnull
	t.Cleanup(func() {
		os.Stdin, os.Stdout = origStdin, origStdout
		devnull.Close()
		tty.Close()
		ptmx.Close()
	})
	return tty
}

func TestTerminalLifecycleTogglesRawMode(t *testing.T) {
	tty := withPTY(t)
	before, err := term.GetState(int(tty.Fd()))
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	tm, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if tm.state == nil {
		t.Fatal("Open did not save the previous terminal state")
	}

	tm.Suspend()
	if tm.state != nil {
		t.Error("Suspend left a saved state behind")
	}
	tm.Suspend() // a second Suspend must be a no-op

	if err := tm.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	tm.Close()

	after, err := term.GetState(int(tty.Fd()))
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if *before != *after {
		t.Error("Close did not restore the original terminal mode")
	}
}

func TestOpenFailsWhenStdinIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	if _, err := Open(); err == nil {
		t.Error("Open succeeded on a pipe; want error")
	}
}

func TestSizeFallsBackWhenStdinIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	if w, h := Size(); w != defaultWidth || h != defaultHeight {
		t.Errorf("Size() = %d, %d; want %d, %d", w, h, defaultWidth, defaultHeight)
	}
}

func TestSizeReportsPTYWindowSize(t *testing.T) {
	tty := withPTY(t)
	if err := pty.Setsize(tty, &pty.Winsize{Cols: 120, Rows: 40}); err != nil {
		t.Skip("pty.Setsize unavailable:", err)
	}
	if w, h := Size(); w != 120 || h != 40 {
		t.Errorf("Size() = %d, %d; want 120, 40", w, h)
	}
}

func TestReadKeyReturnsOneRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	if _, err := w.Write([]byte("\x1b[A")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	key, err := ReadKey()
	if err != nil {
		t.Fatalf("ReadKey: %v", err)
	}
	if string(key) != "\x1b[A" {
		t.Errorf("ReadKey() = %q; want %q", key, "\x1b[A")
	}

	w.Close()
	if _, err := ReadKey(); err == nil {
		t.Error("ReadKey after writer closed succeeded; want EOF")
	}
}

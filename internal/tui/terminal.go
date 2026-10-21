// Package tui holds the terminal plumbing shared by kuroko's full-screen
// views (the log selector and the log viewer): raw-mode/alternate-screen
// lifecycle, key and mouse decoding, line editing for search prompts, and
// small rendering helpers.
//
// Stdin and stdout are looked up on every call rather than captured once,
// so tests can swap os.Stdin/os.Stdout for a PTY.
package tui

import (
	"os"
	"time"

	"golang.org/x/term"
)

const (
	// enterSeq saves the screen, hides the cursor, clears the screen, and
	// enables mouse tracking (SGR 1006).
	enterSeq = "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[?1000h\x1b[?1006h"
	// exitSeq disables mouse tracking, shows the cursor, and restores the
	// main screen.
	exitSeq = "\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[?1049l"

	// settleDelay lets the terminal process the buffer switch and clear
	// before the first frame is drawn.
	settleDelay = 50 * time.Millisecond

	defaultWidth  = 80
	defaultHeight = 24
)

// Terminal is a stdin/stdout pair switched into raw mode on the alternate
// screen. Close must be called to hand the terminal back to the shell.
type Terminal struct {
	state *term.State
}

// Open puts stdin into raw mode and switches stdout to the alternate screen
// with mouse tracking enabled.
func Open() (*Terminal, error) {
	t := &Terminal{}
	if err := t.Resume(); err != nil {
		return nil, err
	}
	return t, nil
}

// Suspend restores the terminal mode saved by Open/Resume without leaving
// the alternate screen, so a nested full-screen view can set the terminal
// up itself. Call Resume afterwards.
func (t *Terminal) Suspend() {
	if t.state != nil {
		_ = term.Restore(stdinFd(), t.state)
		t.state = nil
	}
}

// Resume re-enters raw mode and the alternate screen after Suspend. The
// screen sequence is always re-sent because a nested view disables mouse
// tracking on its own exit.
func (t *Terminal) Resume() error {
	state, err := term.MakeRaw(stdinFd())
	if err != nil {
		return err
	}
	t.state = state
	_, _ = os.Stdout.Write([]byte(enterSeq))
	time.Sleep(settleDelay)
	return nil
}

// Close leaves the alternate screen and restores the original terminal
// mode.
func (t *Terminal) Close() {
	_, _ = os.Stdout.Write([]byte(exitSeq))
	t.Suspend()
}

// Size returns the terminal size, falling back to 80x24 when it cannot be
// determined (e.g. stdin is not a terminal).
func Size() (width, height int) {
	width, height, _ = term.GetSize(stdinFd())
	if width <= 0 || height <= 0 {
		return defaultWidth, defaultHeight
	}
	return width, height
}

func stdinFd() int {
	return int(os.Stdin.Fd())
}

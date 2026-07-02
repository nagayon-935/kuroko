package viewer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

// openPTYOrSkip opens a PTY pair or skips the test if unavailable.
// The caller must close ptmx and tty when done.
func openPTYOrSkip(t *testing.T) (ptmx, tty *os.File) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skip("pty.Open() unavailable:", err)
	}
	return ptmx, tty
}

// withPTYStdin replaces os.Stdin with tty for the duration of t, and
// suppresses os.Stdout to avoid polluting test output with escape codes.
// Returns a cleanup function; call it (or rely on t.Cleanup) to restore.
func withPTYStdin(t *testing.T, tty *os.File) {
	t.Helper()
	origStdin := os.Stdin
	origStdout := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("opening /dev/null:", err)
	}
	os.Stdin = tty
	os.Stdout = devnull
	t.Cleanup(func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
		devnull.Close()
	})
}

// sendKeys writes each sequence to ptmx with a short pause between them so
// the loop drains the read buffer one sequence at a time.
func sendKeys(ptmx *os.File, seqs [][]byte) {
	for _, seq := range seqs {
		ptmx.Write(seq)
		time.Sleep(15 * time.Millisecond)
	}
}

// TestViewerLoopNavigation exercises the major keyboard paths inside
// viewer.loop() using a real PTY so that term.MakeRaw succeeds and we can
// drive the event loop with simulated keystrokes.
func TestViewerLoopNavigation(t *testing.T) {
	ptmx, tty := openPTYOrSkip(t)
	defer ptmx.Close()
	defer tty.Close()
	withPTYStdin(t, tty)

	// Build a log with two commands and output containing "file"
	content := "# kuroko:cmd:2026-06-18T12:00:01+09:00\nuser@host:~$ ls\nfile1.txt\nfile2.txt\n" +
		"# kuroko:cmd:2026-06-18T12:00:02+09:00\nuser@host:~$ cat\nhello world\n"
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "nav.log")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}

	// Key sequences sent after the loop's 50 ms startup sleep.
	seqs := [][]byte{
		// --- Timeline pane navigation ---
		{'j'},          // down: selected → 1
		{'k'},          // up:   selected → 0
		{27, '[', 'B'}, // Down arrow
		{27, '[', 'A'}, // Up arrow (already at 0, clamped)
		// --- Switch to Output pane ---
		{'l'}, // 'l' → PaneOutput
		// --- Output pane scroll ---
		{'j'},               // scroll down
		{27, '[', 'B'},      // Down arrow (scroll)
		{'k'},               // scroll up
		{27, '[', 'A'},      // Up arrow (scroll)
		{4},                 // Ctrl+D: page down
		{21},                // Ctrl+U: page up
		{27, '[', '6', '~'}, // PageDown
		{27, '[', '5', '~'}, // PageUp
		// --- Arrow pane switch ---
		{27, '[', 'D'}, // Left arrow → PaneTimeline
		{27, '[', 'C'}, // Right arrow → PaneOutput
		// --- Tab pane toggle ---
		{9}, // Tab → PaneTimeline
		{9}, // Tab → PaneOutput
		// --- 'h' back to timeline ---
		{'h'}, // → PaneTimeline
		// --- Command search (/) ---
		{'/'},  // enter search mode (SearchCommands)
		{'l'},  // type 'l'
		{'s'},  // type 's' → searchQuery="ls"
		{127},  // Backspace → searchQuery="l"
		{'\r'}, // Enter: confirm filter
		// --- Clear filter ---
		{'/'},  // search mode again
		{'\r'}, // Enter with empty query: restore all
		// --- Output search (f) ---
		{'f'},  // enter output search mode
		{'f'},  // type 'f' → searchQuery="f"
		{'i'},  // type 'i'
		{'l'},  // type 'l'
		{'e'},  // type 'e' → searchQuery="file"
		{127},  // Backspace in SearchOutput → searchQuery="fil"
		{'\r'}, // Enter: confirm → outputQuery="fil"
		// --- Match navigation ---
		{'n'}, // next match (no-op if empty)
		{'N'}, // prev match (no-op if empty)
		// --- Output search cancel ---
		{'f'}, // output search mode again
		{'x'}, // type 'x'
		{27},  // Esc → cancel
		// --- Quit ---
		{'q'},
	}

	go func() {
		time.Sleep(200 * time.Millisecond) // wait for loop startup sleep
		sendKeys(ptmx, seqs)
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- v.loop() }()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("loop() returned unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("loop() did not quit within 10 seconds")
	}
}

// TestViewerLoopCtrlC verifies that Ctrl+C exits the loop cleanly.
func TestViewerLoopCtrlC(t *testing.T) {
	ptmx, tty := openPTYOrSkip(t)
	defer ptmx.Close()
	defer tty.Close()
	withPTYStdin(t, tty)

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "ctrlc.log")
	os.WriteFile(logPath, []byte(testLogContent), 0o600)
	v, _ := newViewer(logPath)

	go func() {
		time.Sleep(200 * time.Millisecond)
		ptmx.Write([]byte{3}) // Ctrl+C
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- v.loop() }()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Ctrl+C: unexpected error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("loop() did not exit on Ctrl+C")
	}
}

// TestViewerLoopSearchCtrlC verifies that Ctrl+C inside search mode exits.
func TestViewerLoopSearchCtrlC(t *testing.T) {
	ptmx, tty := openPTYOrSkip(t)
	defer ptmx.Close()
	defer tty.Close()
	withPTYStdin(t, tty)

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "searchctrlc.log")
	os.WriteFile(logPath, []byte(testLogContent), 0o600)
	v, _ := newViewer(logPath)

	go func() {
		time.Sleep(200 * time.Millisecond)
		sendKeys(ptmx, [][]byte{
			{'/'}, // enter search mode
			{'a'}, // type a char
			{3},   // Ctrl+C inside search → return nil
		})
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- v.loop() }()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Ctrl+C in search: unexpected error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("loop() did not exit on Ctrl+C in search mode")
	}
}

// TestSelectorLoopNavigation exercises selector.loop() via PTY.
func TestSelectorLoopNavigation(t *testing.T) {
	ptmx, tty := openPTYOrSkip(t)
	defer ptmx.Close()
	defer tty.Close()
	withPTYStdin(t, tty)

	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "session1.log"), []byte("log1"), 0o600)
	os.WriteFile(filepath.Join(tmp, "session2.log"), []byte("log2"), 0o600)

	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector(): %v", err)
	}

	seqs := [][]byte{
		// Navigation
		{'j'},          // down
		{'k'},          // up
		{27, '[', 'B'}, // Down arrow
		{27, '[', 'A'}, // Up arrow
		// Filter search
		{'/'},      // enter search mode
		{'s', 'e'}, // type "se" (matches "session*")
		{127},      // Backspace → "s"
		{'\r'},     // Enter: confirm
		// Clear filter
		{'/'},
		{'\r'}, // empty Enter: restore all
		// Search mode Ctrl+C
		{'/'},
		{'a'},
		{3}, // Ctrl+C in search → return nil
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		sendKeys(ptmx, seqs)
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- s.loop() }()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("selector loop() unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("selector loop() did not exit within 10 seconds")
	}
}

// TestSelectorLoopQuit verifies 'q' and ESC exit the selector loop.
func TestSelectorLoopQuit(t *testing.T) {
	ptmx, tty := openPTYOrSkip(t)
	defer ptmx.Close()
	defer tty.Close()
	withPTYStdin(t, tty)

	tmp := t.TempDir()
	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector(): %v", err)
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		ptmx.Write([]byte{'q'})
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- s.loop() }()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("'q': unexpected error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("selector loop() did not exit on 'q'")
	}
}

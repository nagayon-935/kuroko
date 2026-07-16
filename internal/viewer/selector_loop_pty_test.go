package viewer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSelectorReenablesMouseAfterSubViewer proves the mouse-tracking escape
// sequence is re-sent after returning from the sub-viewer (C2). The
// selector's loop() disables the alternate screen/mouse tracking before
// handing the terminal to the nested Run() call and re-establishes its own
// screen afterward; before the fix it restored the alternate screen but
// forgot to re-enable mouse tracking, leaving wheel/click navigation dead in
// the selector for the remainder of the process.
func TestSelectorReenablesMouseAfterSubViewer(t *testing.T) {
	ptmx, tty := openPTYOrSkip(t)
	defer ptmx.Close()
	defer tty.Close()

	origStdin := os.Stdin
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(): %v", err)
	}
	os.Stdin = tty
	os.Stdout = w
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
	}()

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "20260618_sub.log")
	if err := os.WriteFile(logPath, []byte(testLogContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector(): %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- s.loop() }()

	time.Sleep(80 * time.Millisecond)
	sendKeys(ptmx, [][]byte{{13}}) // Enter: open the sub-viewer
	time.Sleep(80 * time.Millisecond)
	sendKeys(ptmx, [][]byte{{'q'}}) // quit the sub-viewer, returning to the selector
	time.Sleep(80 * time.Millisecond)
	sendKeys(ptmx, [][]byte{{'q'}}) // quit the selector

	select {
	case loopErr := <-done:
		if loopErr != nil {
			t.Fatalf("loop() error: %v", loopErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("selector loop() did not return after quitting")
	}

	w.Close()
	data, _ := io.ReadAll(r)
	r.Close()
	output := string(data)

	const enableSeq = "\x1b[?1000h\x1b[?1006h"
	count := strings.Count(output, enableSeq)
	if count < 2 {
		t.Errorf("mouse-enable sequence %q appeared %d times; want >= 2 (once at startup, once after returning from sub-viewer)", enableSeq, count)
	}
}

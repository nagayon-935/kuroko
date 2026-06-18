package viewer

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testLogContent matches the format written by internal/logger.
const testLogContent = `# kuroko session log
# Started : 2026-06-18T12:00:00+09:00
# Command : ssh host
# --------------------------------------------------------------------

# kuroko:cmd:2026-06-18T12:00:01+09:00
user@host:~$ ls
file.txt

# kuroko:cmd:2026-06-18T12:00:02+09:00
user@host:~$ cat file.txt
hello world

# --------------------------------------------------------------------
# Ended   : 2026-06-18T12:00:03+09:00
# Exit    : 0
`

func TestNewViewer(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "test.log")
	if err := os.WriteFile(logPath, []byte(testLogContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer() error: %v", err)
	}

	if len(v.allCmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(v.allCmds))
	}
	if v.allCmds[0].Command != "ls" {
		t.Errorf("allCmds[0].Command = %q; want %q", v.allCmds[0].Command, "ls")
	}
	if v.allCmds[1].Command != "cat file.txt" {
		t.Errorf("allCmds[1].Command = %q; want %q", v.allCmds[1].Command, "cat file.txt")
	}
	// Timestamps should be parsed from the metadata lines
	if v.allCmds[0].Timestamp == "" {
		t.Error("allCmds[0].Timestamp should not be empty")
	}
	// Initial filter with empty query returns all commands
	if len(v.filteredIdx) != 2 {
		t.Errorf("expected 2 filtered entries initially, got %d", len(v.filteredIdx))
	}
}

func TestNewViewerFileNotFound(t *testing.T) {
	_, err := newViewer("/nonexistent/path/file.log")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestNewViewerGzip(t *testing.T) {
	tmp := t.TempDir()
	gzPath := filepath.Join(tmp, "test.log.gz")

	f, err := os.Create(gzPath)
	if err != nil {
		t.Fatalf("create gzip file: %v", err)
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write([]byte(testLogContent)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	gw.Close()
	f.Close()

	v, err := newViewer(gzPath)
	if err != nil {
		t.Fatalf("newViewer() for gzip: %v", err)
	}
	if len(v.allCmds) != 2 {
		t.Errorf("expected 2 commands in gzip log, got %d", len(v.allCmds))
	}
}

func TestUpdateFilter(t *testing.T) {
	v := &Viewer{
		allCmds: []CommandMetadata{
			{Command: "ls"},
			{Command: "cat file.txt"},
			{Command: "vim config.go"},
		},
	}

	// Empty query: all commands included
	v.searchQuery = ""
	v.updateFilter()
	if len(v.filteredIdx) != 3 {
		t.Errorf("empty query: expected 3 results, got %d", len(v.filteredIdx))
	}

	// Case-insensitive substring search
	v.searchQuery = "CAT"
	v.updateFilter()
	if len(v.filteredIdx) != 1 {
		t.Errorf("'CAT' query: expected 1, got %d", len(v.filteredIdx))
	}
	if v.filteredIdx[0] != 1 {
		t.Errorf("'CAT' filter result: expected index 1, got %d", v.filteredIdx[0])
	}

	// Multi-match: "." is in "cat file.txt" and "vim config.go" but not "ls"
	v.searchQuery = "."
	v.updateFilter()
	if len(v.filteredIdx) != 2 {
		t.Errorf("'.' query: expected 2, got %d", len(v.filteredIdx))
	}

	// No match: filteredIdx is empty, selected is clamped to 0
	v.searchQuery = "xxxxxxxx"
	v.selected = 5
	v.updateFilter()
	if len(v.filteredIdx) != 0 {
		t.Errorf("no-match query: expected 0, got %d", len(v.filteredIdx))
	}
	if v.selected != 0 {
		t.Errorf("no-match: selected should clamp to 0, got %d", v.selected)
	}
}

func TestParseMetadataOldFormat(t *testing.T) {
	// Backwards compatibility: "timestamp|command" format
	content := "# kuroko:cmd:2026-01-01T12:00:00+09:00|ls\nuser@host:~$ ls\nfile.txt\n"
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "old.log")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	if len(v.allCmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(v.allCmds))
	}
	if v.allCmds[0].Command != "ls" {
		t.Errorf("command = %q; want %q", v.allCmds[0].Command, "ls")
	}
}

func TestParseMetadataNoPrompt(t *testing.T) {
	// When the line after the metadata has no shell prompt, the raw line is used as command.
	content := "# kuroko:cmd:2026-01-01T12:00:00+09:00\nno-prompt-command\n"
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "noprompt.log")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	if len(v.allCmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(v.allCmds))
	}
	if v.allCmds[0].Command != "no-prompt-command" {
		t.Errorf("command = %q; want %q", v.allCmds[0].Command, "no-prompt-command")
	}
}

// TestViewerDraw verifies draw() does not panic when stdout is not a real terminal.
// term.GetSize returns 0,0 on a non-TTY, so the fallback (80×24) is exercised.
func TestViewerDraw(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "test.log")
	if err := os.WriteFile(logPath, []byte(testLogContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	v.draw() // must not panic
}

// TestViewerDrawInSearch exercises the search-mode footer branch in draw().
func TestViewerDrawInSearch(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "test.log")
	if err := os.WriteFile(logPath, []byte(testLogContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	v.inSearch = true
	v.searchQuery = "ls"
	v.draw()
}

// TestViewerDrawEmpty exercises draw() with no commands (empty viewer).
func TestViewerDrawEmpty(t *testing.T) {
	v := &Viewer{logPath: "/dev/null"}
	v.draw()
}

// TestViewerDrawLongCommandTruncation exercises the left-pane truncation path.
func TestViewerDrawLongCommandTruncation(t *testing.T) {
	longCmd := strings.Repeat("x", 50) // > leftWidth (30), triggers truncation
	content := fmt.Sprintf("# kuroko:cmd:2026-06-18T12:00:01+09:00\nuser@host:~$ %s\noutput\n", longCmd)
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "longcmd.log")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	v.draw()
}

// TestViewerDrawLongOutputTruncation exercises the right-pane truncation path.
func TestViewerDrawLongOutputTruncation(t *testing.T) {
	longOutput := strings.Repeat("y", 100) // > rightWidth (49), triggers truncation
	content := fmt.Sprintf("# kuroko:cmd:2026-06-18T12:00:01+09:00\nuser@host:~$ ls\n%s\n", longOutput)
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "longout.log")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	v.draw()
}

// TestViewerRunNonTerminal calls Run on a real log file; in go test, stdin is
// not a TTY so loop() fails immediately at term.MakeRaw, returning an error.
func TestViewerRunNonTerminal(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "test.log")
	if err := os.WriteFile(logPath, []byte(testLogContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := Run(logPath)
	if err == nil {
		t.Error("expected error from Run when stdin is not a terminal")
	}
}

func TestViewerScrollAndFocus(t *testing.T) {
	v := &Viewer{
		activePane: PaneTimeline,
	}
	if v.activePane != PaneTimeline {
		t.Errorf("expected PaneTimeline, got %v", v.activePane)
	}

	v.outputScroll = 10
	v.selected = 1
	v.filteredIdx = []int{0, 1}
	v.allCmds = []CommandMetadata{{Offset: 0}, {Offset: 10}}
	v.logData = []byte(testLogContent)
	v.updateOutput()
	if v.outputScroll != 0 {
		t.Errorf("expected outputScroll to be reset to 0, got %d", v.outputScroll)
	}
}

func TestViewerOutputSearch(t *testing.T) {
	v := &Viewer{
		currentOutputLines: []string{
			"hello world",
			"test message",
			"HELLO anti-gravity",
			"some other output",
		},
	}

	v.outputQuery = "hello"
	v.updateMatches()

	if len(v.matchLines) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(v.matchLines))
	}
	if v.matchLines[0] != 0 || v.matchLines[1] != 2 {
		t.Errorf("expected match lines at 0 and 2, got %v", v.matchLines)
	}
	if v.activeMatch != 0 {
		t.Errorf("expected activeMatch to be 0, got %d", v.activeMatch)
	}

	v.scrollToLine(v.matchLines[0], 5)
	if v.outputScroll != 0 {
		t.Errorf("expected outputScroll to be 0, got %d", v.outputScroll)
	}
}

func TestSelectorScanAndFilter(t *testing.T) {
	tmp := t.TempDir()

	files := []string{"a.log", "b.log.gz", "c.txt"}
	for _, f := range files {
		path := filepath.Join(tmp, f)
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatalf("writing test file: %v", err)
		}
	}

	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector error: %v", err)
	}

	if len(s.items) != 2 {
		t.Errorf("expected 2 selector items, got %d", len(s.items))
	}

	s.searchQuery = "b.log"
	s.updateFilter()
	if len(s.filtered) != 1 {
		t.Errorf("expected 1 filtered item, got %d", len(s.filtered))
	}
	if s.filtered[0].name != "b.log.gz" {
		t.Errorf("expected b.log.gz, got %s", s.filtered[0].name)
	}
}

func TestHighlightQuery(t *testing.T) {
	line := "Hello World"
	query := "world"
	res := highlightQuery(line, query, false)

	expected := "Hello \x1b[30;43mWorld\x1b[0m"
	if res != expected {
		t.Errorf("expected %q, got %q", expected, res)
	}

	resActive := highlightQuery(line, query, true)
	expectedActive := "Hello \x1b[30;42mWorld\x1b[0m"
	if resActive != expectedActive {
		t.Errorf("expected %q, got %q", expectedActive, resActive)
	}
}

package viewer

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ryu/kuroko/internal/textwidth"
)

// ansiEscapeRegexp strips SGR/CSI escape sequences (colors, cursor moves,
// line-clear) from captured draw() output so tests can inspect the plain
// text that would actually occupy terminal columns.
var ansiEscapeRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string {
	return ansiEscapeRegexp.ReplaceAllString(s, "")
}

// captureStdout temporarily redirects os.Stdout to an in-memory pipe so a
// draw() call's rendered output can be inspected. draw() only needs
// os.Stdout to be a valid io.Writer (not a real terminal), so this works
// without a PTY.
func captureStdout(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(): %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	return func() string {
		os.Stdout = orig
		w.Close()
		data, _ := io.ReadAll(r)
		r.Close()
		return string(data)
	}
}

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

// TestViewerDrawScrollsToKeepSelectionVisible proves the timeline pane
// scrolls to reveal the selected command when it falls outside the first
// screenful of rows. Before the C1 fix, draw() always rendered
// filteredIdx[0:bodyHeight] regardless of v.selected, so a selection past
// the visible window was never shown no matter how far navigation moved.
func TestViewerDrawScrollsToKeepSelectionVisible(t *testing.T) {
	var sb strings.Builder
	const numCmds = 30 // comfortably exceeds the default 80x24 fallback's ~21-row body
	for i := 0; i < numCmds; i++ {
		fmt.Fprintf(&sb, "# kuroko:cmd:2026-06-18T12:%02d:00+09:00\nuser@host:~$ cmd%d\nout%d\n\n", i, i, i)
	}
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "many.log")
	if err := os.WriteFile(logPath, []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	if len(v.allCmds) != numCmds {
		t.Fatalf("expected %d commands, got %d", numCmds, len(v.allCmds))
	}

	v.selected = len(v.filteredIdx) - 1 // select the last command

	restore := captureStdout(t)
	v.draw()
	output := restore()

	lastCmd := v.allCmds[v.filteredIdx[v.selected]].Command
	if !strings.Contains(output, lastCmd) {
		t.Errorf("draw() output missing selected command %q; scrolling did not bring it into view", lastCmd)
	}
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
	tests := []struct {
		name     string
		line     string
		query    string
		isActive bool
		want     string
	}{
		{"empty query", "hello", "", false, "hello"},
		{"active match", "Hello World", "world", true, "Hello \x1b[30;42mWorld\x1b[0m"},
		{"inactive match", "Hello World", "world", false, "Hello \x1b[30;43mWorld\x1b[0m"},
		{"no match", "hello world", "xyz", false, "hello world"},
		{"multiple occurrences", "file1 file2", "file", false,
			"\x1b[30;43mfile\x1b[0m1 \x1b[30;43mfile\x1b[0m2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := highlightQuery(tt.line, tt.query, tt.isActive)
			if got != tt.want {
				t.Errorf("highlightQuery(%q, %q, %v) = %q; want %q",
					tt.line, tt.query, tt.isActive, got, tt.want)
			}
		})
	}
}

// TestHighlightQueryCaseFoldingByteLengthMismatch covers a line containing
// U+212A (KELVIN SIGN), whose strings.ToLower result ("k", 1 byte) is
// shorter than the original rune's UTF-8 encoding (3 bytes). Before the C3
// fix, highlightQuery located the match in the lowercased copy and sliced
// that byte offset directly into the original (differently-sized) string,
// which can panic with "slice bounds out of range" or slice mid-rune. The
// fix must produce valid UTF-8 and never panic, regardless of the exact
// highlighting choice for this edge case.
func TestHighlightQueryCaseFoldingByteLengthMismatch(t *testing.T) {
	line := "Kelvin" // "Kelvin" spelled with the Kelvin sign as the leading rune
	got := highlightQuery(line, "k", false)
	if !utf8.ValidString(got) {
		t.Errorf("highlightQuery(%q, %q, false) = %q; not valid UTF-8", line, "k", got)
	}
}

func TestScrollToLineBranches(t *testing.T) {
	lines := make([]string, 100)
	v := &Viewer{currentOutputLines: lines}
	bodyHeight := 20

	// targetLine < 0: no-op
	v.outputScroll = 5
	v.scrollToLine(-1, bodyHeight)
	if v.outputScroll != 5 {
		t.Errorf("negative target: scroll = %d; want 5", v.outputScroll)
	}

	// targetLine >= len(currentOutputLines): no-op
	v.outputScroll = 5
	v.scrollToLine(100, bodyHeight)
	if v.outputScroll != 5 {
		t.Errorf("out-of-range target: scroll = %d; want 5", v.outputScroll)
	}

	// targetLine in visible window [scroll, scroll+bodyHeight): no change
	v.outputScroll = 10
	v.scrollToLine(15, bodyHeight)
	if v.outputScroll != 10 {
		t.Errorf("in-range target: scroll = %d; want 10", v.outputScroll)
	}

	// targetLine before visible window: scroll back (result < 0 → clamped to 0)
	v.outputScroll = 30
	v.scrollToLine(2, bodyHeight)
	// 2 - (20/2) = -8 → clamped to 0
	if v.outputScroll != 0 {
		t.Errorf("before-range clamp: scroll = %d; want 0", v.outputScroll)
	}

	// targetLine after visible window: scroll forward
	v.outputScroll = 0
	v.scrollToLine(50, bodyHeight)
	// 50 - (20/2) = 40
	if v.outputScroll != 40 {
		t.Errorf("after-range: scroll = %d; want 40", v.outputScroll)
	}
}

func TestUpdateMatchesNoMatch(t *testing.T) {
	v := &Viewer{
		currentOutputLines: []string{"hello", "world"},
		activeMatch:        0,
	}
	// Non-empty query that matches nothing → activeMatch = -1
	v.outputQuery = "xyzzy_nomatch"
	v.updateMatches()
	if len(v.matchLines) != 0 {
		t.Errorf("no-match: len(matchLines) = %d; want 0", len(v.matchLines))
	}
	if v.activeMatch != -1 {
		t.Errorf("no-match: activeMatch = %d; want -1", v.activeMatch)
	}
}

// TestViewerDrawOutputPane exercises the PaneOutput branch in draw():
// highlighted border and output-pane footer (no matches).
func TestViewerDrawOutputPane(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "test.log")
	if err := os.WriteFile(logPath, []byte(testLogContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	v.activePane = PaneOutput
	v.draw()
}

// TestViewerDrawWithMatches exercises the match-count footer and highlightQuery
// paths (active + non-active) when outputQuery has multiple hits.
func TestViewerDrawWithMatches(t *testing.T) {
	// Build log content where two output lines both match the query.
	content := "# kuroko:cmd:2026-06-18T12:00:01+09:00\nuser@host:~$ ls\nfile1.txt\nfile2.txt\n"
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "multi.log")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	v.activePane = PaneOutput
	v.outputQuery = "file"
	v.updateMatches()
	// Two matches: activeMatch=0 (active), matchLines[1] (non-active)
	if len(v.matchLines) < 2 {
		t.Skipf("expected ≥2 matches for 'file', got %d — skipping draw test", len(v.matchLines))
	}
	v.draw()
}

// TestViewerDrawSearchOutputMode exercises the "Find in output" footer branch.
func TestViewerDrawSearchOutputMode(t *testing.T) {
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
	v.searchMode = SearchOutput
	v.searchQuery = "file"
	v.draw()
}

func TestTruncateDisplay(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"shorter than width is unchanged", "abc", 10, "abc"},
		{"exact width is unchanged", "abcde", 5, "abcde"},
		{"ascii truncates with ellipsis", "abcdefghij", 5, "ab..."},
		{
			"multi-byte input truncates on rune boundaries, not bytes",
			// Each 接/続/先 is a 3-byte, display-width-2 UTF-8 rune; only
			// one fits within the 2-column budget left after reserving 3
			// columns for "...".
			"接続先router1",
			5,
			"接...",
		},
		{"multi-byte input shorter than width is unchanged", "接続先", 10, "接続先"},
		{"width of exactly 3 truncates with no room for ellipsis", "abcdefgh", 3, "abc"},
		{"width less than 3 truncates with no ellipsis", "abcdefgh", 2, "ab"},
		{"width zero returns empty string", "abcdefgh", 0, ""},
		{"negative width returns empty string", "abcdefgh", -1, ""},
		{"empty input returns empty string", "", 5, ""},
		{
			// C4: wide characters occupy 2 terminal columns, so truncation
			// must be based on display width, not rune count, or the right
			// border of the pane drifts out of alignment.
			"wide characters truncate by display width, not rune count",
			"漢字テスト",
			4,
			"...",
		},
		{"wide characters exactly filling width are unchanged", "漢字", 4, "漢字"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateDisplay(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("truncateDisplay(%q, %d) = %q; want %q", tt.input, tt.width, got, tt.want)
			}
			// truncateDisplay must never split a multi-byte rune: the
			// result must always be valid UTF-8.
			if !utf8.ValidString(got) {
				t.Errorf("truncateDisplay(%q, %d) = %q; not valid UTF-8", tt.input, tt.width, got)
			}
			if tt.width > 0 {
				if n := textwidth.String(got); n > tt.width {
					t.Errorf("truncateDisplay(%q, %d) = %q; display width %d exceeds requested width %d", tt.input, tt.width, got, n, tt.width)
				}
			}
		})
	}
}

// TestViewerDrawHeaderAndFooterAlignWithWideCharacters proves the header and
// footer bars (which embed a file path and, while filtering, the user's
// search query) pad and truncate by terminal display width rather than byte
// length. Before the C4 fix, header/footer construction used len() (bytes)
// and a raw header[:v.width] byte slice, which drifts out of alignment or
// slices mid-rune whenever the embedded path or query contains full-width
// Japanese characters.
func TestViewerDrawHeaderAndFooterAlignWithWideCharacters(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "スイッチ01.log")
	if err := os.WriteFile(logPath, []byte(testLogContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := newViewer(logPath)
	if err != nil {
		t.Fatalf("newViewer(): %v", err)
	}
	v.width, v.height = 80, 24
	v.inSearch = true
	v.searchMode = SearchCommands
	v.searchQuery = "接続確認スイッチ"

	restore := captureStdout(t)
	v.draw()
	output := restore()

	for i, line := range strings.Split(stripANSI(output), "\r\n") {
		if line == "" {
			continue
		}
		if n := textwidth.String(line); n != v.width {
			t.Errorf("rendered line %d %q has display width %d; want exactly %d", i, line, n, v.width)
		}
	}
}

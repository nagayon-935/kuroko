package viewer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/textwidth"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatSize(tt.size)
			if got != tt.want {
				t.Errorf("formatSize(%d) = %q; want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestNewSelector(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "20260618_test.log"), []byte("log content"), 0o600)
	os.WriteFile(filepath.Join(tmp, "20260618_other.log.gz"), []byte("gz content"), 0o600)
	os.WriteFile(filepath.Join(tmp, "not_a_log.txt"), []byte("txt"), 0o600) // excluded

	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector(): %v", err)
	}
	if len(s.items) != 2 {
		t.Errorf("expected 2 items, got %d", len(s.items))
	}
}

func TestNewSelectorEmpty(t *testing.T) {
	tmp := t.TempDir()
	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector() empty dir: %v", err)
	}
	if len(s.items) != 0 {
		t.Errorf("expected 0 items, got %d", len(s.items))
	}
}

func TestNewSelectorBadDir(t *testing.T) {
	_, err := newSelector("/nonexistent/path/to/dir")
	if err == nil {
		t.Error("expected error for non-existent dir, got nil")
	}
}

func TestScanLogsWithSubdir(t *testing.T) {
	tmp := t.TempDir()
	// Subdirectory must be ignored by scanLogs
	os.Mkdir(filepath.Join(tmp, "subdir"), 0o700)
	os.WriteFile(filepath.Join(tmp, "test.log"), []byte("log"), 0o600)

	s := &LogSelector{logDir: tmp}
	if err := s.scanLogs(); err != nil {
		t.Fatalf("scanLogs(): %v", err)
	}
	if len(s.items) != 1 {
		t.Errorf("expected 1 item (subdir ignored), got %d", len(s.items))
	}
}

func TestScanLogsNonMatchingFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "data.csv"), []byte("a,b"), 0o600) // not .log or .log.gz
	os.WriteFile(filepath.Join(tmp, "run.log"), []byte("log"), 0o600)

	s := &LogSelector{logDir: tmp}
	if err := s.scanLogs(); err != nil {
		t.Fatalf("scanLogs(): %v", err)
	}
	if len(s.items) != 1 {
		t.Errorf("expected 1 item (.csv excluded), got %d", len(s.items))
	}
	if s.items[0].name != "run.log" {
		t.Errorf("items[0].name = %q; want %q", s.items[0].name, "run.log")
	}
}

func TestSelectorUpdateFilter(t *testing.T) {
	s := &LogSelector{
		items: []selectorItem{
			{name: "session_ssh.log", modTime: time.Now(), size: 100},
			{name: "session_git.log", modTime: time.Now(), size: 200},
			{name: "session_vim.log", modTime: time.Now(), size: 300},
		},
	}

	// Empty query: all items returned
	s.searchQuery = ""
	s.updateFilter()
	if len(s.filtered) != 3 {
		t.Errorf("empty query: expected 3, got %d", len(s.filtered))
	}

	// Matching query (case-insensitive)
	s.searchQuery = "SSH"
	s.updateFilter()
	if len(s.filtered) != 1 {
		t.Errorf("'SSH' query: expected 1, got %d", len(s.filtered))
	}

	// No-match query: filtered empty, selected clamped to 0
	s.selected = 5
	s.searchQuery = "xyzzy_nomatch"
	s.updateFilter()
	if len(s.filtered) != 0 {
		t.Errorf("no-match: expected 0, got %d", len(s.filtered))
	}
	if s.selected != 0 {
		t.Errorf("no-match clamp: selected = %d; want 0", s.selected)
	}

	// selected >= len(filtered): clamp to len-1
	s.searchQuery = "ssh"
	s.selected = 10
	s.updateFilter()
	if s.selected != 0 { // only 1 result, so clamped to 0
		t.Errorf("selected clamp: selected = %d; want 0", s.selected)
	}
}

// TestSelectorDraw calls draw() directly; term.GetSize returns 0 on a non-TTY
// so the fallback (80×24) is exercised.
func TestSelectorDraw(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "20260618_ssh_host.log"), []byte("log"), 0o600)
	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector(): %v", err)
	}
	s.draw() // must not panic
}

func TestSelectorDrawEmpty(t *testing.T) {
	s := &LogSelector{logDir: "/dev/null"}
	s.draw()
}

func TestSelectorDrawInSearch(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "test.log"), []byte("log"), 0o600)
	s, _ := newSelector(tmp)
	s.inSearch = true
	s.searchQuery = "test"
	s.draw()
}

func TestSelectorDrawLongLine(t *testing.T) {
	// Very long file name to trigger line truncation inside draw()
	tmp := t.TempDir()
	longName := strings.Repeat("x", 100) + ".log"
	os.WriteFile(filepath.Join(tmp, longName), []byte("log"), 0o600)
	s, _ := newSelector(tmp)
	s.draw()
}

// TestSelectorDrawHeaderAndFooterAlignWithWideCharacters mirrors
// TestViewerDrawHeaderAndFooterAlignWithWideCharacters for LogSelector: the
// header embeds s.logDir and the footer embeds s.searchQuery while
// filtering, both of which can contain full-width Japanese characters (a
// log directory under a Japanese hostname, or a filter query typed by the
// user). Before the C4 fix, header/footer padding used len() (bytes) and
// sliced footerText[:s.width] by byte offset, drifting the row's true
// display width away from s.width whenever wide characters were present.
func TestSelectorDrawHeaderAndFooterAlignWithWideCharacters(t *testing.T) {
	tmp := t.TempDir()
	logDir := filepath.Join(tmp, "ログ保存先")
	if err := os.Mkdir(logDir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "20260618_ssh_host.log"), []byte("log"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := newSelector(logDir)
	if err != nil {
		t.Fatalf("newSelector(): %v", err)
	}
	s.width, s.height = 80, 24
	s.inSearch = true
	s.searchQuery = "接続確認スイッチ"

	restore := captureStdout(t)
	s.draw()
	output := restore()

	for i, line := range strings.Split(stripANSI(output), "\r\n") {
		if line == "" {
			continue
		}
		if n := textwidth.String(line); n != s.width {
			t.Errorf("rendered line %d %q has display width %d; want exactly %d", i, line, n, s.width)
		}
	}
}

// TestRunSelectorNonTerminal verifies RunSelector propagates the MakeRaw error
// that occurs when stdin is not a TTY (which is always the case under go test).
// TestSelectorDrawScrollsToKeepSelectionVisible mirrors
// TestViewerDrawScrollsToKeepSelectionVisible for LogSelector's single log
// list: before the C1 fix, draw() always rendered items[0:bodyHeight]
// regardless of s.selected, so a selection past the visible window was
// never shown.
func TestSelectorDrawScrollsToKeepSelectionVisible(t *testing.T) {
	tmp := t.TempDir()
	const numLogs = 30 // comfortably exceeds the default 80x24 fallback's ~21-row body
	for i := 0; i < numLogs; i++ {
		name := fmt.Sprintf("20260618_%03d_target.log", i)
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("log"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	s, err := newSelector(tmp)
	if err != nil {
		t.Fatalf("newSelector(): %v", err)
	}
	if len(s.filtered) != numLogs {
		t.Fatalf("expected %d items, got %d", numLogs, len(s.filtered))
	}

	s.selected = len(s.filtered) - 1 // select the last item

	restore := captureStdout(t)
	s.draw()
	output := restore()

	lastName := s.filtered[s.selected].name
	if !strings.Contains(output, lastName) {
		t.Errorf("draw() output missing selected item %q; scrolling did not bring it into view", lastName)
	}
}

func TestRunSelectorNonTerminal(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{LogDir: tmp}
	err := RunSelector(cfg)
	if err == nil {
		t.Error("expected error when stdin is not a terminal")
	}
}

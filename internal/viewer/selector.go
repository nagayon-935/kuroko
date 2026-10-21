package viewer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logger"
	"github.com/ryu/kuroko/internal/logstore"
	"github.com/ryu/kuroko/internal/textwidth"
	"github.com/ryu/kuroko/internal/tui"
)

type selectorItem struct {
	name      string
	modTime   time.Time
	size      int64
	summary   logstore.Summary
	readError bool
}

type LogSelector struct {
	logDir      string
	items       []selectorItem
	filtered    []selectorItem
	selected    int
	searchQuery string
	inSearch    bool
	sortDesc    bool // true = newest first (default), false = oldest first
	width       int
	height      int
	listScroll  int
}

func RunSelector(cfg *config.Config) error {
	s, err := newSelector(cfg.LogDir)
	if err != nil {
		return err
	}
	return s.loop()
}

func newSelector(logDir string) (*LogSelector, error) {
	s := &LogSelector{
		logDir:   logDir,
		sortDesc: true, // newest first by default
	}
	if err := s.scanLogs(); err != nil {
		return nil, err
	}
	s.updateFilter()
	return s, nil
}

func (s *LogSelector) scanLogs() error {
	entries, err := logstore.ListLogFiles(s.logDir)
	if err != nil {
		return err
	}

	var items []selectorItem
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		summary, readErr := logstore.ReadSummary(entry.Path)
		if summary.Target == "" && summary.Command != "" {
			summary.Target = logger.TargetAddress(strings.Fields(summary.Command))
		}
		items = append(items, selectorItem{
			name:      entry.RelativePath,
			modTime:   info.ModTime(),
			size:      info.Size(),
			summary:   summary,
			readError: readErr != nil,
		})
	}

	s.items = items
	s.applySortOrder(s.items)
	return nil
}

func (s *LogSelector) applySortOrder(items []selectorItem) {
	sort.Slice(items, func(i, j int) bool {
		ti, tj := items[i].started(), items[j].started()
		if ti.Equal(tj) {
			ti, tj = items[i].modTime, items[j].modTime
		}
		if ti.Equal(tj) {
			if s.sortDesc {
				return items[i].name > items[j].name
			}
			return items[i].name < items[j].name
		}
		if s.sortDesc {
			return ti.After(tj)
		}
		return ti.Before(tj)
	})
}

func (s *LogSelector) toggleSort() {
	s.sortDesc = !s.sortDesc
	s.applySortOrder(s.items)
	s.updateFilter()
	s.selected = 0
}

func (s *LogSelector) updateFilter() {
	s.filtered = nil
	query := strings.ToLower(s.searchQuery)
	for _, item := range s.items {
		searchText := item.name + " " + item.summary.Target + " " + item.summary.Command + " " + item.status()
		if query == "" || strings.Contains(strings.ToLower(searchText), query) {
			s.filtered = append(s.filtered, item)
		}
	}
	if s.selected >= len(s.filtered) {
		s.selected = len(s.filtered) - 1
	}
	if s.selected < 0 {
		s.selected = 0
	}
}

func (s *LogSelector) moveSelection(delta int) {
	next := s.selected + delta
	if next >= 0 && next < len(s.filtered) {
		s.selected = next
	}
}

func (s *LogSelector) loop() error {
	t, err := tui.Open()
	if err != nil {
		return err
	}
	defer t.Close()

	for {
		s.draw()

		key, err := tui.ReadKey()
		if err != nil {
			return err
		}
		if len(key) == 0 {
			continue
		}

		if m, ok := tui.ParseMouse(key); ok {
			s.handleMouse(m)
			continue
		}

		if s.inSearch {
			query, res := tui.EditLine(s.searchQuery, key)
			switch res {
			case tui.EditSubmit, tui.EditCancel:
				s.inSearch = false
			case tui.EditInterrupt:
				return nil
			case tui.EditChanged:
				s.searchQuery = query
				s.updateFilter()
			}
			continue
		}

		if len(key) == 1 {
			switch key[0] {
			case 'q', tui.KeyEsc, tui.KeyCtrlC:
				return nil
			case 'j': // Down
				s.moveSelection(1)
			case 'k': // Up
				s.moveSelection(-1)
			case 's': // Toggle sort order
				s.toggleSort()
			case '/': // Filter mode
				s.inSearch = true
				s.searchQuery = ""
				s.updateFilter()
			case tui.KeyCR, tui.KeyLF: // View log
				if err := s.openSelected(t); err != nil {
					return err
				}
			}
		} else if c, ok := tui.CSIKey(key); ok {
			switch c {
			case 'A': // Up
				s.moveSelection(-1)
			case 'B': // Down
				s.moveSelection(1)
			}
		}
	}
}

func (s *LogSelector) handleMouse(m tui.Mouse) {
	switch m.Button {
	case tui.MouseWheelUp:
		s.moveSelection(-1)
	case tui.MouseWheelDown:
		s.moveSelection(1)
	case tui.MouseLeft:
		if idx := tui.ClickRowToIndex(m.Y-3, s.listScroll, len(s.filtered)); idx >= 0 {
			s.selected = idx
		}
	}
}

// openSelected runs the log viewer for the selected entry, handing it the
// terminal and taking it back afterwards.
func (s *LogSelector) openSelected(t *tui.Terminal) error {
	if len(s.filtered) == 0 {
		return nil
	}
	fullPath := filepath.Join(s.logDir, s.filtered[s.selected].name)

	// The sub-viewer sets up raw mode itself.
	t.Suspend()
	viewErr := Run(fullPath)
	if err := t.Resume(); err != nil {
		return err
	}
	if viewErr != nil {
		fmt.Fprintf(os.Stderr, "\r\n[kuroko] viewer error: %v\r\n", viewErr)
	}

	// Re-scan in case logs changed
	if serr := s.scanLogs(); serr != nil {
		fmt.Fprintf(os.Stderr, "\r\n[kuroko] rescan error: %v\r\n", serr)
	}
	s.updateFilter()
	return nil
}

// bodyHeight returns the number of rows available for the log list,
// reserving chromeHeight rows for the header, spacer, and footer.
func (s *LogSelector) bodyHeight() int {
	h := s.height - chromeHeight
	if h < 1 {
		h = 1
	}
	return h
}

func (s *LogSelector) draw() {
	s.width, s.height = tui.Size()

	var out bytes.Buffer
	out.WriteString(tui.Home)

	bodyHeight := s.bodyHeight()
	s.listScroll = tui.FollowSelection(s.selected, s.listScroll, bodyHeight)

	// Draw Header
	header := textwidth.Fit(fmt.Sprintf(" kuroko logs selector  [ Dir: %s ]", s.logDir), s.width)
	out.WriteString(tui.Inverse(header) + tui.ClearEOL + "\r\n")
	out.WriteString(tui.ClearEOL + "\r\n")

	// Render items
	for r := 0; r < bodyHeight; r++ {
		var line string
		listRow := r + s.listScroll
		if listRow < len(s.filtered) {
			item := s.filtered[listRow]
			indicator := "  "
			if listRow == s.selected {
				indicator = "> "
			}

			ts := item.started().Format("2006-01-02 15:04:05")
			sizeStr := formatSize(item.size)

			// Try to align nicely
			if item.summary.Command != "" {
				duration := "--"
				if !item.summary.Started.IsZero() && !item.summary.Ended.IsZero() {
					duration = item.summary.Duration().Round(time.Second).String()
				}
				line = fmt.Sprintf("%s[%s] %-8s %-7s %s  (%s) %s", indicator, ts, item.status(), duration, item.summary.Target, sizeStr, item.name)
			} else {
				line = fmt.Sprintf("%s[%s] (%-8s) %s", indicator, ts, sizeStr, item.name)
				if item.readError {
					line = fmt.Sprintf("%s[%s] unreadable (%s) %s", indicator, ts, sizeStr, item.name)
				}
			}
			line = textwidth.Fit(line, s.width)

			if listRow == s.selected {
				line = tui.Inverse(line)
			}
		} else if r == 0 && len(s.filtered) == 0 {
			line = "No saved logs. Record a session with: kuroko <command>"
			if len(s.items) > 0 {
				line = "No matching logs. Press / then Enter to clear the filter."
			}
			line = textwidth.Fit(line, s.width)
		} else {
			line = strings.Repeat(" ", s.width)
		}
		out.WriteString(line + tui.ClearEOL + "\r\n")
	}

	out.WriteString(tui.MoveToRow(s.height))

	// Draw Footer
	sortLabel := "new→old"
	if !s.sortDesc {
		sortLabel = "old→new"
	}
	footerText := fmt.Sprintf(" [j/k/Arrows]: Navigate  [Enter]: View  [s]: Sort: %s  [/]: Filter  [q]: Quit", sortLabel)
	if s.inSearch {
		footerText = fmt.Sprintf(" Filter logs (Enter to confirm): %s_", s.searchQuery)
	}
	footerText = textwidth.Fit(footerText, s.width)
	out.WriteString(tui.Inverse(footerText) + tui.ClearEOL)

	os.Stdout.Write(out.Bytes())
}

func (item selectorItem) started() time.Time {
	if !item.summary.Started.IsZero() {
		return item.summary.Started
	}
	return item.modTime
}

func (item selectorItem) status() string {
	if item.readError {
		return "unreadable"
	}
	if item.summary.ExitCode == nil {
		if !item.summary.Started.IsZero() {
			return "open"
		}
		return "unknown"
	}
	if *item.summary.ExitCode == 0 {
		return "OK"
	}
	return fmt.Sprintf("exit %d", *item.summary.ExitCode)
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

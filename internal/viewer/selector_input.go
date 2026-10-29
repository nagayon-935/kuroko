package viewer

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nagayon-935/kuroko/internal/tui"
)

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

		s.statusMessage = ""
		var quit bool
		if m, ok := tui.ParseMouse(key); ok {
			s.handleMouse(m)
		} else if s.inSearch {
			quit = s.handleSearchKey(key)
		} else if len(key) == 1 {
			quit, err = s.handleKey(t, key[0])
			if err != nil {
				return err
			}
		} else {
			s.handleEscapeKey(key)
		}
		if quit {
			return nil
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

// handleSearchKey edits the filter prompt and reports whether the selector
// should quit.
func (s *LogSelector) handleSearchKey(key []byte) (quit bool) {
	query, res := tui.EditLine(s.searchQuery, key)
	switch res {
	case tui.EditSubmit, tui.EditCancel:
		s.inSearch = false
	case tui.EditInterrupt:
		return true
	case tui.EditChanged:
		s.searchQuery = query
		s.updateFilter()
	}
	return false
}

// handleKey applies a single-byte key in normal mode and reports whether
// the selector should quit.
func (s *LogSelector) handleKey(t *tui.Terminal, b byte) (quit bool, err error) {
	switch b {
	case 'q', tui.KeyEsc, tui.KeyCtrlC:
		return true, nil
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
		return false, s.openSelected(t)
	}
	return false, nil
}

// handleEscapeKey applies the up/down arrow keys.
func (s *LogSelector) handleEscapeKey(key []byte) {
	if c, ok := tui.CSIKey(key); ok {
		switch c {
		case 'A': // Up
			s.moveSelection(-1)
		case 'B': // Down
			s.moveSelection(1)
		}
	}
}

// openSelected runs the log viewer for the selected entry, handing it the
// terminal and taking it back afterwards. Viewer and rescan failures are
// reported in the footer; only failing to reclaim the terminal is fatal.
func (s *LogSelector) openSelected(t *tui.Terminal) error {
	if s.selected < 0 || s.selected >= len(s.filtered) {
		return nil
	}
	fullPath := filepath.Join(s.logDir, s.filtered[s.selected].name)

	// The sub-viewer sets up raw mode itself.
	t.Suspend()
	viewErr := Run(fullPath)
	if err := t.Resume(); err != nil {
		return err
	}

	var problems []string
	if viewErr != nil {
		problems = append(problems, fmt.Sprintf("viewer error: %v", viewErr))
	}
	// Re-scan in case logs changed
	if serr := s.scanLogs(); serr != nil {
		problems = append(problems, fmt.Sprintf("rescan error: %v", serr))
	}
	s.statusMessage = strings.Join(problems, "; ")
	s.updateFilter()
	return nil
}

package viewer

import "github.com/nagayon-935/kuroko/internal/tui"

func (v *Viewer) loop() error {
	t, err := tui.Open()
	if err != nil {
		return err
	}
	defer t.Close()

	for {
		v.draw()

		key, err := tui.ReadKey()
		if err != nil {
			return err
		}
		if len(key) == 0 {
			continue
		}

		v.statusMessage = ""
		var quit bool
		if m, ok := tui.ParseMouse(key); ok {
			v.handleMouse(m)
		} else if v.inSearch {
			quit = v.handleSearchKey(key)
		} else if len(key) == 1 {
			quit = v.handleKey(key[0])
		} else {
			v.handleEscapeKey(key)
		}
		if quit {
			return nil
		}
	}
}

func (v *Viewer) handleMouse(m tui.Mouse) {
	leftWidth, _ := paneWidths(v.width)
	inTimeline := m.X <= leftWidth
	inOutput := m.X > leftWidth+1

	switch m.Button {
	case tui.MouseWheelUp:
		if inTimeline {
			v.moveTimeline(-1)
		} else if inOutput {
			v.scrollOutput(-3)
		}
	case tui.MouseWheelDown:
		if inTimeline {
			v.moveTimeline(1)
		} else if inOutput {
			v.scrollOutput(3)
		}
	case tui.MouseLeft:
		if m.Release {
			return
		}
		if inTimeline {
			v.activePane = PaneTimeline
			if idx := tui.ClickRowToIndex(m.Y-3, v.timelineScroll, len(v.filteredIdx)); idx >= 0 {
				v.selected = idx
				v.updateOutput()
			}
		} else if inOutput {
			v.activePane = PaneOutput
		}
	}
}

// handleSearchKey edits the search prompt and reports whether the viewer
// should quit.
func (v *Viewer) handleSearchKey(key []byte) (quit bool) {
	query, res := tui.EditLine(v.searchQuery, key)
	switch res {
	case tui.EditSubmit:
		v.inSearch = false
		if v.searchMode == SearchOutput {
			v.outputQuery = v.searchQuery
			v.searchQuery = v.savedCmdFilter
			v.updateMatches()
			if len(v.matchLines) > 0 {
				v.scrollToLine(v.matchLines[v.activeMatch], v.bodyHeight())
			}
		}
	case tui.EditCancel:
		v.inSearch = false
		v.searchQuery = v.savedCmdFilter
		if v.searchMode == SearchCommands {
			v.updateFilter()
			v.updateOutput()
		} else {
			v.outputQuery = v.savedOutputQuery
			v.updateMatches()
		}
	case tui.EditInterrupt:
		return true
	case tui.EditChanged:
		v.searchQuery = query
		v.onSearchQueryChanged()
	}
	return false
}

// handleKey applies a single-byte key in normal mode and reports whether
// the viewer should quit.
func (v *Viewer) handleKey(b byte) (quit bool) {
	switch b {
	case 'e': // Export the selected command or entire session
		v.exportSelection()
	case 'q', tui.KeyEsc, tui.KeyCtrlC:
		return true
	case tui.KeyTab:
		if v.activePane == PaneTimeline {
			v.activePane = PaneOutput
		} else {
			v.activePane = PaneTimeline
		}
	case 'h':
		v.activePane = PaneTimeline
	case 'l':
		v.activePane = PaneOutput
	case 'j': // down
		v.stepActivePane(1, 3)
	case 'k': // up
		v.stepActivePane(-1, -3)
	case 'g': // top of active pane
		v.jumpActivePane(false)
	case 'G': // bottom of active pane
		v.jumpActivePane(true)
	case tui.KeyCtrlD:
		v.pageActivePane(1)
	case tui.KeyCtrlU:
		v.pageActivePane(-1)
	case '/': // Search Commands mode
		v.startSearch(SearchCommands)
	case 'f': // Search Output mode
		v.startSearch(SearchOutput)
	case 'n': // Next output search match
		v.stepMatch(1)
	case 'N': // Previous output search match
		v.stepMatch(-1)
	}
	return false
}

// handleEscapeKey applies arrow keys (Esc [ A..D), Home/End, and PageUp/PageDown.
func (v *Viewer) handleEscapeKey(key []byte) {
	if tui.IsHomeKey(key) {
		v.jumpActivePane(false)
		return
	}
	if tui.IsEndKey(key) {
		v.jumpActivePane(true)
		return
	}
	if dir := tui.PageDir(key); dir != 0 {
		v.pageActivePane(dir)
		return
	}
	if c, ok := tui.CSIKey(key); ok {
		switch c {
		case 'A': // Up arrow
			v.stepActivePane(-1, -3)
		case 'B': // Down arrow
			v.stepActivePane(1, 3)
		case 'C': // Right arrow
			v.activePane = PaneOutput
		case 'D': // Left arrow
			v.activePane = PaneTimeline
		}
	}
}

// pageActivePane scrolls the active pane by one page in direction dir (+1 down, -1 up).
func (v *Viewer) pageActivePane(dir int) {
	page := dir * v.bodyHeight()
	if v.activePane == PaneOutput {
		v.scrollOutput(page)
		return
	}
	if len(v.filteredIdx) > 0 {
		next := max(0, min(v.selected+page, len(v.filteredIdx)-1))
		if next != v.selected {
			v.selected = next
			v.updateOutput()
		}
	}
}

func (v *Viewer) jumpActivePane(toEnd bool) {
	if v.activePane == PaneOutput {
		if toEnd {
			v.outputScroll = max(0, len(v.currentOutputLines)-v.bodyHeight())
		} else {
			v.outputScroll = 0
		}
		return
	}
	if len(v.filteredIdx) == 0 {
		return
	}
	target := 0
	if toEnd {
		target = len(v.filteredIdx) - 1
	}
	if target != v.selected {
		v.selected = target
		v.updateOutput()
	}
}

func (v *Viewer) startSearch(mode SearchMode) {
	v.savedCmdFilter = v.searchQuery
	v.savedOutputQuery = v.outputQuery
	v.inSearch = true
	v.searchMode = mode
	v.searchQuery = ""
	if mode == SearchCommands {
		v.updateFilter()
		v.updateOutput()
	}
}

// stepMatch moves to the next (dir=+1) or previous (dir=-1) output search
// match, wrapping around.
func (v *Viewer) stepMatch(dir int) {
	if len(v.matchLines) == 0 || v.activeMatch == -1 {
		return
	}
	n := len(v.matchLines)
	v.activeMatch = (v.activeMatch + dir + n) % n
	v.scrollToLine(v.matchLines[v.activeMatch], v.bodyHeight())
}

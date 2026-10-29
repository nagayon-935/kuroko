package viewer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nagayon-935/kuroko/internal/textwidth"
	"github.com/nagayon-935/kuroko/internal/tui"
)

// Keep both panes usable on narrow terminals; wide terminals retain the
// original minimum timeline width. Mouse hit testing uses the same layout.
func paneWidths(width int) (left, right int) {
	if width < 3 {
		return 0, max(0, width-1)
	}
	left = width * 35 / 100
	if width >= 80 {
		left = max(left, 30)
	}
	left = max(1, min(left, width-2))
	return left, width - left - 1
}

func (v *Viewer) draw() {
	v.width, v.height = tui.Size()

	// Move cursor to home (don't clear screen to prevent flickering)
	var out bytes.Buffer
	out.WriteString(tui.Home)

	// Left pane: 35% of width, min 30 chars
	leftWidth, rightWidth := paneWidths(v.width)
	bodyHeight := v.bodyHeight()
	v.timelineScroll = tui.FollowSelection(v.selected, v.timelineScroll, bodyHeight)

	out.WriteString(tui.Inverse(textwidth.Fit(v.headerText(), v.width)) + tui.ClearEOL + "\r\n")
	// Empty line between header and body
	out.WriteString(tui.ClearEOL + "\r\n")

	// Middle border is highlighted while the output pane is active
	border := "|"
	if v.activePane == PaneOutput {
		border = tui.Yellow("|")
	}
	for r := 0; r < bodyHeight; r++ {
		out.WriteString(v.timelineCell(r, leftWidth) + border + v.outputCell(r, rightWidth) + tui.ClearEOL + "\r\n")
	}

	// Move cursor explicitly to the bottom line for footer to prevent screen scrolling
	out.WriteString(tui.MoveToRow(v.height))
	out.WriteString(tui.Inverse(textwidth.Fit(v.footerText(), v.width)) + tui.ClearEOL)

	os.Stdout.Write(out.Bytes())
}

// headerText names the file and marks the active pane.
func (v *Viewer) headerText() string {
	paneStr := " PANE: [Timeline]  Output"
	if v.activePane == PaneOutput {
		paneStr = " PANE:  Timeline  [Output]"
	}
	return fmt.Sprintf(" kuroko log viewer  [ File: %s ]%s", tui.Plain(filepath.Base(v.logPath)), paneStr)
}

// timelineCell renders body row r of the timeline (command list) pane,
// exactly width columns wide.
func (v *Viewer) timelineCell(r, width int) string {
	timelineRow := r + v.timelineScroll
	if timelineRow >= len(v.filteredIdx) {
		return strings.Repeat(" ", width)
	}
	cmdIdx := v.filteredIdx[timelineRow]
	cmd := v.allCmds[cmdIdx]

	ts := ""
	if t, err := time.Parse(time.RFC3339, cmd.Timestamp); err == nil {
		ts = t.Format("15:04:05")
	}

	indicator := "  "
	if timelineRow == v.selected {
		indicator = "> "
	}

	var text string
	if cmd.WholeSession {
		text = indicator + "[Session] Entire session"
	} else {
		text = fmt.Sprintf("%s[%s] %d. %s", indicator, ts, cmdIdx, tui.Plain(cmd.Command))
	}
	text = textwidth.Fit(text, width)

	if timelineRow == v.selected {
		text = tui.Inverse(text)
	}
	return text
}

// outputCell renders body row r of the output pane, exactly width columns
// wide, highlighting the search query on matching lines.
func (v *Viewer) outputCell(r, width int) string {
	lineIdx := r + v.outputScroll
	if lineIdx >= len(v.currentOutputLines) {
		return strings.Repeat(" ", width)
	}
	text := textwidth.Fit(tui.Plain(strings.ReplaceAll(v.currentOutputLines[lineIdx], "\r", "")), width)

	for idx, ml := range v.matchLines {
		if ml == lineIdx {
			return highlightQuery(text, v.outputQuery, idx == v.activeMatch)
		}
	}
	return text
}

// footerText shows key hints for the active pane, the search prompt while
// typing, or a transient status message.
func (v *Viewer) footerText() string {
	if v.statusMessage != "" {
		return " " + tui.Plain(v.statusMessage)
	}
	if v.inSearch {
		if v.searchMode == SearchCommands {
			return fmt.Sprintf(" Search commands (Enter to confirm): %s_", tui.Plain(v.searchQuery))
		}
		return fmt.Sprintf(" Find in output (Enter to confirm): %s_", tui.Plain(v.searchQuery))
	}
	if v.activePane == PaneTimeline {
		return " [e]: Export  [Tab]: Pane  [j/k]: Move  [/]: Search Cmd  [f]: Find  [q]: Quit"
	}
	if len(v.matchLines) > 0 {
		return fmt.Sprintf(" [e]: Export  [Tab]: Pane  [j/k/PgUpDown]: Scroll  [n/N]: %d/%d  [f]: Find  [q]: Quit", v.activeMatch+1, len(v.matchLines))
	}
	return " [e]: Export  [Tab]: Pane  [j/k/PgUpDown]: Scroll  [f]: Find  [q]: Quit"
}

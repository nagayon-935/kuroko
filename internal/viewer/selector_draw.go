package viewer

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ryu/kuroko/internal/textwidth"
	"github.com/ryu/kuroko/internal/tui"
)

func (s *LogSelector) draw() {
	s.width, s.height = tui.Size()

	var out bytes.Buffer
	out.WriteString(tui.Home)

	bodyHeight := s.bodyHeight()
	s.listScroll = tui.FollowSelection(s.selected, s.listScroll, bodyHeight)

	header := fmt.Sprintf(" kuroko logs selector  [ Dir: %s ]", tui.Plain(s.logDir))
	out.WriteString(tui.Inverse(textwidth.Fit(header, s.width)) + tui.ClearEOL + "\r\n")
	out.WriteString(tui.ClearEOL + "\r\n")

	for r := 0; r < bodyHeight; r++ {
		out.WriteString(s.rowText(r) + tui.ClearEOL + "\r\n")
	}

	out.WriteString(tui.MoveToRow(s.height))
	out.WriteString(tui.Inverse(textwidth.Fit(s.footerText(), s.width)) + tui.ClearEOL)

	os.Stdout.Write(out.Bytes())
}

// rowText renders body row r of the log list, exactly s.width columns wide.
// The first row of an empty list explains why it is empty.
func (s *LogSelector) rowText(r int) string {
	listRow := r + s.listScroll
	if listRow >= len(s.filtered) {
		if r == 0 && len(s.filtered) == 0 {
			line := "No saved logs. Record a session with: kuroko <command>"
			if len(s.items) > 0 {
				line = "No matching logs. Press / then Enter to clear the filter."
			}
			return textwidth.Fit(line, s.width)
		}
		return strings.Repeat(" ", s.width)
	}

	item := s.filtered[listRow]
	indicator := "  "
	if listRow == s.selected {
		indicator = "> "
	}
	ts := item.started().Format("2006-01-02 15:04:05")
	sizeStr := formatSize(item.size)
	name := tui.Plain(item.name)

	var line string
	switch {
	case item.summary.Command != "":
		duration := "--"
		if !item.summary.Started.IsZero() && !item.summary.Ended.IsZero() {
			duration = item.summary.Duration().Round(time.Second).String()
		}
		line = fmt.Sprintf("%s[%s] %-8s %-7s %s  (%s) %s", indicator, ts, item.status(), duration, tui.Plain(item.summary.Target), sizeStr, name)
	case item.readError:
		line = fmt.Sprintf("%s[%s] unreadable (%s) %s", indicator, ts, sizeStr, name)
	default:
		line = fmt.Sprintf("%s[%s] (%-8s) %s", indicator, ts, sizeStr, name)
	}
	line = textwidth.Fit(line, s.width)

	if listRow == s.selected {
		line = tui.Inverse(line)
	}
	return line
}

// footerText shows key hints, the filter prompt while typing, or a
// transient status message.
func (s *LogSelector) footerText() string {
	if s.statusMessage != "" {
		return " " + tui.Plain(s.statusMessage)
	}
	if s.inSearch {
		return fmt.Sprintf(" Filter logs (Enter to confirm): %s_", tui.Plain(s.searchQuery))
	}
	sortLabel := "new→old"
	if !s.sortDesc {
		sortLabel = "old→new"
	}
	return fmt.Sprintf(" [j/k/Arrows]: Navigate  [Enter]: View  [s]: Sort: %s  [/]: Filter  [q]: Quit", sortLabel)
}

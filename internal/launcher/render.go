package launcher

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/ryu/kuroko/internal/textwidth"
	"github.com/ryu/kuroko/internal/tui"
)

// plain prevents config values and filenames from emitting terminal controls.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func (p *prompt) drawMenu(title string, items []string, selected, scroll, previewScroll, width, height int, query string, manual, back bool) int {
	// Leave the last column unused to avoid autowrap at the terminal edge.
	width = max(1, width-1)
	height = max(5, height)
	body := height - 4
	listWidth := width
	preview := "Select a connection."
	if len(items) > 0 {
		item := items[selected]
		switch title {
		case "SSH host":
			preview = p.previews[item]
		case "Serial device":
			preview = "Device: " + item + "\nBaud rate: choose on the next screen (default 115200)"
		default:
			preview = map[string]string{"ssh": "SSH connection\nChoose a Host from ~/.ssh/config or enter a host manually.", "screen": "Serial connection\nChoose a USB serial device and baud rate."}[item]
		}
	} else {
		preview = "No matching candidates. Type to filter, Ctrl+U to clear, Ctrl+N to enter manually."
	}
	listHeight, previewHeight := body, body
	previewWidth := width
	if width >= 70 {
		listWidth = width / 3
		previewWidth = width - listWidth - 3
	} else {
		listHeight = max(1, body/2)
		previewHeight = max(1, body-listHeight-1)
	}
	lines := wrapPreview(preview, previewWidth)
	scroll = tui.FollowSelection(selected, scroll, listHeight)
	previewScroll = min(previewScroll, max(0, len(lines)-previewHeight))
	rows := make([]string, height)
	rows[0] = title
	rows[1] = "Filter: " + plain(query) + "_"
	if p.notice != "" {
		rows[1] += "  " + plain(p.notice)
	}
	rows[2] = "Candidates"
	if width >= 70 {
		rows[2] = textwidth.Fit(rows[2], listWidth) + " | Preview"
	}
	for row := 0; row < body; row++ {
		list := ""
		if row < listHeight {
			index := scroll + row
			if index < len(items) {
				list = "  " + plain(items[index])
				if index == selected {
					list = "> " + plain(items[index])
				}
			} else if row == 0 {
				list = "(no candidates)"
			}
		}
		if width >= 70 {
			right := ""
			if i := previewScroll + row; i < len(lines) {
				right = plain(lines[i])
			}
			left := textwidth.Fit(list, listWidth)
			if scroll+row == selected && len(items) > 0 {
				left = tui.Inverse(left)
			}
			rows[row+3] = left + " | " + textwidth.Fit(right, max(1, width-listWidth-3))
		} else if row < listHeight {
			rows[row+3] = textwidth.Fit(list, width)
			if scroll+row == selected && len(items) > 0 {
				rows[row+3] = tui.Inverse(rows[row+3])
			}
		} else if row == listHeight {
			rows[row+3] = "--- Preview ---"
		} else if i := previewScroll + row - listHeight - 1; i < len(lines) {
			rows[row+3] = plain(lines[i])
		}
	}
	footer := "↑↓ move  Enter OK"
	if manual {
		footer += "  ^N manual"
	}
	if back {
		footer += "  ^B back"
	}
	footer += "  ^C quit  PgUp/Dn preview"
	if width < 70 {
		footer = "↑↓ Enter  ^N manual ^B back ^C quit"
	}

	rows[height-1] = footer
	var frame strings.Builder
	frame.WriteString(tui.Home)
	for i, row := range rows {
		// Rows with ANSI highlights have already been sized before styling.
		if i >= 3 && i < height-1 && strings.Contains(row, "\x1b[") {
			frame.WriteString(row)
		} else {
			frame.WriteString(textwidth.Fit(row, width))
		}
		frame.WriteString(tui.ClearEOL)
		if i < height-1 {
			frame.WriteString("\r\n")
		}
	}
	fmt.Fprint(p.out, frame.String())
	return previewScroll
}

func wrapPreview(preview string, width int) []string {
	var lines []string
	for _, line := range strings.Split(preview, "\n") {
		var part strings.Builder
		columns := 0
		for _, r := range plain(line) {
			n := textwidth.Rune(r)
			if columns+n > width && part.Len() > 0 {
				lines = append(lines, part.String())
				part.Reset()
				columns = 0
			}
			part.WriteRune(r)
			columns += n
		}
		lines = append(lines, part.String())
	}
	return lines
}

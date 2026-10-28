package launcher

import (
	"fmt"
	"io"
	"strings"

	"github.com/ryu/kuroko/internal/textwidth"
	"github.com/ryu/kuroko/internal/tui"
)

const wideMenuThreshold = 70

func listBodyHeight(width, height int) int {
	_, _, _, _, listHeight, _, _ := menuLayout(width, height)
	return listHeight
}

func menuLayout(width, height int) (cols, rows, body, listWidth, listHeight, previewWidth, previewHeight int) {
	// Leave the last column unused to avoid autowrap at the terminal edge.
	cols = max(1, width-1)
	rows = max(5, height)
	body = rows - 4
	if cols >= wideMenuThreshold {
		listWidth = cols / 3
		previewWidth = max(1, cols-listWidth-3)
		return cols, rows, body, listWidth, body, previewWidth, body
	}
	listHeight = max(1, body/2)
	previewHeight = max(1, body-listHeight-1)
	return cols, rows, body, cols, listHeight, cols, previewHeight
}

func (p *prompt) previewText(title string, items []string, selected int) string {
	if len(items) == 0 {
		return "No matching candidates. Type to filter, Ctrl+U to clear, Ctrl+N to enter manually."
	}
	item := items[selected]
	switch title {
	case "SSH host":
		return p.previews[item]
	case "Serial device":
		return "Device: " + item + "\nBaud rate: choose on the next screen (default 115200)"
	default:
		switch item {
		case "ssh":
			return "SSH connection\nChoose a Host from ~/.ssh/config or enter a host manually."
		case "screen":
			return "Serial connection\nChoose a USB serial device and baud rate."
		default:
			return "Select a connection."
		}
	}
}

func (p *prompt) drawMenu(title string, items []string, selected, scroll, previewScroll, width, height int, query string, manual, back bool) int {
	cols, totalRows, body, listWidth, listHeight, previewWidth, previewHeight := menuLayout(width, height)
	if len(items) > 0 {
		selected = min(max(0, selected), len(items)-1)
	} else {
		selected = 0
	}
	lines := wrapPreview(p.previewText(title, items, selected), previewWidth)
	scroll = tui.FollowSelection(selected, scroll, listHeight)
	previewScroll = min(max(0, previewScroll), max(0, len(lines)-previewHeight))

	rows := make([]string, totalRows)
	rows[0] = title
	rows[1] = "Filter: " + tui.Plain(query) + "_"
	if p.notice != "" {
		rows[1] += "  " + tui.Plain(p.notice)
	}
	rows[2] = "Candidates"
	if cols >= wideMenuThreshold {
		rows[2] = textwidth.Fit(rows[2], listWidth) + " | Preview"
	}
	renderBodyRows(rows[3:3+body], items, lines, selected, scroll, previewScroll, cols, listWidth, listHeight, previewWidth)
	rows[totalRows-1] = menuFooter(cols, manual, back)
	writeMenuFrame(p.out, rows, cols)
	return previewScroll
}

func renderBodyRows(dst, items, lines []string, selected, scroll, previewScroll, cols, listWidth, listHeight, previewWidth int) {
	for row := range dst {
		list := ""
		if row < listHeight {
			list = listRowText(items, scroll+row, selected, row)
		}
		isSelected := scroll+row == selected && len(items) > 0
		if cols >= wideMenuThreshold {
			right := ""
			if i := previewScroll + row; i < len(lines) {
				right = tui.Plain(lines[i])
			}
			left := textwidth.Fit(list, listWidth)
			if isSelected {
				left = tui.Inverse(left)
			}
			dst[row] = left + " | " + textwidth.Fit(right, previewWidth)
		} else if row < listHeight {
			dst[row] = textwidth.Fit(list, cols)
			if isSelected {
				dst[row] = tui.Inverse(dst[row])
			}
		} else if row == listHeight {
			dst[row] = "--- Preview ---"
		} else if i := previewScroll + row - listHeight - 1; i < len(lines) {
			dst[row] = tui.Plain(lines[i])
		}
	}
}

func listRowText(items []string, index, selected, row int) string {
	if index < len(items) {
		prefix := "  "
		if index == selected {
			prefix = "> "
		}
		return prefix + tui.Plain(items[index])
	}
	if row == 0 {
		return "(no candidates)"
	}
	return ""
}

func menuFooter(cols int, manual, back bool) string {
	if cols < wideMenuThreshold {
		return "↑↓ Enter  ^N manual ^B back ^C quit"
	}
	footer := "↑↓ move  Enter OK"
	if manual {
		footer += "  ^N manual"
	}
	if back {
		footer += "  ^B back"
	}
	return footer + "  ^C quit  PgUp/Dn preview"
}

func writeMenuFrame(out io.Writer, rows []string, width int) {
	var frame strings.Builder
	frame.WriteString(tui.Home)
	for i, row := range rows {
		// Rows with ANSI highlights have already been sized before styling.
		if i >= 3 && i < len(rows)-1 && strings.Contains(row, "\x1b[") {
			frame.WriteString(row)
		} else {
			frame.WriteString(textwidth.Fit(row, width))
		}
		frame.WriteString(tui.ClearEOL)
		if i < len(rows)-1 {
			frame.WriteString("\r\n")
		}
	}
	fmt.Fprint(out, frame.String())
}

func wrapPreview(preview string, width int) []string {
	var lines []string
	for _, line := range strings.Split(preview, "\n") {
		var part strings.Builder
		columns := 0
		for _, r := range tui.Plain(line) {
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

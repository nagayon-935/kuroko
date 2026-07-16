package viewer

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/ryu/kuroko/internal/logger"
	"github.com/ryu/kuroko/internal/textwidth"
)

// chromeHeight is the number of terminal rows consumed by the header, the
// blank spacer line, and the footer, leaving the remainder for the body.
const chromeHeight = 3

// truncateDisplay truncates s to at most width terminal columns, appending
// "..." when truncated. Width is measured with textwidth.String rather than
// rune count, since East Asian Wide/Fullwidth characters (e.g. Japanese
// hostnames or device output) occupy two columns each; truncating by rune
// count alone drifts the pane's right border out of alignment. Truncation
// always stops on a rune boundary, so the result is valid UTF-8.
func truncateDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if textwidth.String(s) <= width {
		return s
	}
	budget := width
	suffix := ""
	if width > 3 {
		budget = width - 3
		suffix = "..."
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		w := textwidth.Rune(r)
		if col+w > budget {
			break
		}
		b.WriteRune(r)
		col += w
	}
	return b.String() + suffix
}

type CommandMetadata struct {
	Timestamp string `json:"timestamp"`
	Command   string `json:"command"`
	Offset    int64  `json:"offset"`
}

type Pane int

const (
	PaneTimeline Pane = iota
	PaneOutput
)

type SearchMode int

const (
	SearchCommands SearchMode = iota
	SearchOutput
)

var mouseRegexp = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)([Mm])`)

type Viewer struct {
	logPath            string
	logData            []byte
	allCmds            []CommandMetadata
	filteredIdx        []int // Indices into allCmds matching current search
	selected           int   // Index into filteredIdx
	searchQuery        string
	inSearch           bool
	searchMode         SearchMode
	outputQuery        string
	matchLines         []int // Indices of command output lines matching outputQuery
	activeMatch        int   // Index into matchLines
	width              int
	height             int
	activePane         Pane
	outputScroll       int
	timelineScroll     int
	currentOutputLines []string
}

func Run(logPath string) error {
	v, err := newViewer(logPath)
	if err != nil {
		return err
	}
	return v.loop()
}

func newViewer(logPath string) (*Viewer, error) {
	// Read log file (decompressing if gzip)
	f, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	defer f.Close()

	var logData []byte
	if strings.HasSuffix(logPath, ".gz") {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("reading gzip: %w", err)
		}
		defer gr.Close()
		logData, err = io.ReadAll(gr)
	} else {
		logData, err = io.ReadAll(f)
	}
	if err != nil {
		return nil, fmt.Errorf("reading log data: %w", err)
	}

	v := &Viewer{
		logPath:     logPath,
		logData:     logData,
		activePane:  PaneTimeline,
		activeMatch: -1,
	}

	v.parseMetadata()
	v.updateFilter()
	v.updateOutput()
	return v, nil
}

func (v *Viewer) updateOutput() {
	v.currentOutputLines = nil
	v.matchLines = nil
	v.activeMatch = -1
	v.outputScroll = 0

	if len(v.filteredIdx) == 0 {
		return
	}

	actualIdx := v.filteredIdx[v.selected]
	cmd := v.allCmds[actualIdx]
	offset := cmd.Offset

	// End offset is the next command's offset or end of log data
	var endOffset int64 = int64(len(v.logData))
	if actualIdx < len(v.allCmds)-1 {
		endOffset = v.allCmds[actualIdx+1].Offset
	}

	if offset < int64(len(v.logData)) && endOffset <= int64(len(v.logData)) && offset <= endOffset {
		cmdOutput := string(v.logData[offset:endOffset])
		rawLines := strings.Split(cmdOutput, "\n")
		for _, rl := range rawLines {
			// Strip metadata comments so they are not shown on TUI
			if strings.HasPrefix(rl, "# kuroko:cmd:") {
				continue
			}
			v.currentOutputLines = append(v.currentOutputLines, rl)
		}
	}

	v.updateMatches()
}

func (v *Viewer) updateMatches() {
	v.matchLines = nil
	if v.outputQuery == "" {
		return
	}
	query := strings.ToLower(v.outputQuery)
	for i, line := range v.currentOutputLines {
		if strings.Contains(strings.ToLower(line), query) {
			v.matchLines = append(v.matchLines, i)
		}
	}
	if len(v.matchLines) > 0 {
		v.activeMatch = 0
	} else {
		v.activeMatch = -1
	}
}

// bodyHeight returns the number of rows available for the timeline/output
// body, reserving chromeHeight rows for the header, spacer, and footer.
func (v *Viewer) bodyHeight() int {
	h := v.height - chromeHeight
	if h < 1 {
		h = 1
	}
	return h
}

func (v *Viewer) scrollToLine(targetLine int, bodyHeight int) {
	if targetLine < 0 || targetLine >= len(v.currentOutputLines) {
		return
	}
	if targetLine < v.outputScroll || targetLine >= v.outputScroll+bodyHeight {
		v.outputScroll = targetLine - (bodyHeight / 2)
		if v.outputScroll < 0 {
			v.outputScroll = 0
		}
	}
}

// highlightQuery wraps case-insensitive matches of query in line with ANSI
// highlight codes. Matching is done rune-by-rune with unicode.ToLower rather
// than via strings.ToLower(line) + strings.Index: ToLower can change a
// string's byte length (e.g. U+212A KELVIN SIGN → "k"), which would make an
// index found in the lowercased copy invalid to slice directly out of the
// original (differently-sized) string — a byte-offset table keyed by rune
// position keeps every slice bound valid by construction.
func highlightQuery(line string, query string, isActive bool) string {
	if query == "" {
		return line
	}
	queryRunes := []rune(query)
	lineRunes := []rune(line)

	byteOffsets := make([]int, len(lineRunes)+1)
	offset := 0
	for i, r := range lineRunes {
		byteOffsets[i] = offset
		offset += utf8.RuneLen(r)
	}
	byteOffsets[len(lineRunes)] = offset

	var result strings.Builder
	lastByte := 0
	for i := 0; i <= len(lineRunes)-len(queryRunes); i++ {
		if !runesEqualFold(lineRunes[i:i+len(queryRunes)], queryRunes) {
			continue
		}
		startByte, endByte := byteOffsets[i], byteOffsets[i+len(queryRunes)]
		result.WriteString(line[lastByte:startByte])
		matchText := line[startByte:endByte]
		if isActive {
			fmt.Fprintf(&result, "\x1b[30;42m%s\x1b[0m", matchText)
		} else {
			fmt.Fprintf(&result, "\x1b[30;43m%s\x1b[0m", matchText)
		}
		lastByte = endByte
		i += len(queryRunes) - 1
	}
	result.WriteString(line[lastByte:])
	return result.String()
}

func runesEqualFold(a, b []rune) bool {
	for i := range a {
		if unicode.ToLower(a[i]) != unicode.ToLower(b[i]) {
			return false
		}
	}
	return true
}

func (v *Viewer) parseMetadata() {
	var cmds []CommandMetadata
	var currentOffset int64 = 0

	// Split by newline to scan line by line
	lines := bytes.Split(v.logData, []byte("\n"))
	for i, line := range lines {
		lineLen := int64(len(line)) + 1 // +1 for the newline character

		lineStr := string(line)

		prefix := "# kuroko:cmd:"
		if strings.HasPrefix(lineStr, prefix) {
			payload := lineStr[len(prefix):]

			var timestamp string
			var command string

			// For backwards compatibility, handle old format "timestamp|command"
			if idx := strings.Index(payload, "|"); idx >= 0 {
				timestamp = payload[:idx]
				command = payload[idx+1:]
			} else {
				timestamp = payload
				// Extract command from the next line (prompt + command)
				if i+1 < len(lines) {
					_, cmdBytes := logger.SplitPrompt(lines[i+1])
					if len(cmdBytes) > 0 {
						command = string(cmdBytes)
					} else {
						command = string(lines[i+1])
					}
				}
			}

			command = strings.TrimSpace(strings.ReplaceAll(command, "\r", ""))

			cmds = append(cmds, CommandMetadata{
				Timestamp: timestamp,
				Command:   command,
				Offset:    currentOffset,
			})
		}
		currentOffset += lineLen
	}
	v.allCmds = cmds
}

func (v *Viewer) updateFilter() {
	v.filteredIdx = nil
	query := strings.ToLower(v.searchQuery)
	for i, c := range v.allCmds {
		if query == "" || strings.Contains(strings.ToLower(c.Command), query) {
			v.filteredIdx = append(v.filteredIdx, i)
		}
	}
	if v.selected >= len(v.filteredIdx) {
		v.selected = len(v.filteredIdx) - 1
	}
	if v.selected < 0 {
		v.selected = 0
	}
}

func (v *Viewer) loop() error {
	// Enter raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Save screen, hide cursor, clear screen, and enable mouse tracking (SGR 1006)
	_, _ = os.Stdout.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[?1000h\x1b[?1006h"))
	defer func() {
		_, _ = os.Stdout.Write([]byte("\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[?1049l"))
	}()

	// Sleep slightly to allow the terminal to process the buffer switch and clear screen
	time.Sleep(50 * time.Millisecond)

	for {
		v.draw()

		// Read key input
		buf := make([]byte, 16)
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}

		key := buf[:n]
		if bytes.HasPrefix(key, []byte("\x1b[<")) {
			m := mouseRegexp.FindSubmatch(key)
			if len(m) == 5 {
				btn, _ := strconv.Atoi(string(m[1]))
				x, _ := strconv.Atoi(string(m[2]))
				y, _ := strconv.Atoi(string(m[3]))

				leftWidth := (v.width * 35) / 100
				if leftWidth < 30 {
					leftWidth = 30
				}
				bodyHeight := v.bodyHeight()

				if btn == 64 { // Scroll Up
					if x <= leftWidth {
						if v.selected > 0 {
							v.selected--
							v.updateOutput()
						}
					} else if x > leftWidth+1 {
						v.outputScroll -= 3
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				} else if btn == 65 { // Scroll Down
					if x <= leftWidth {
						if v.selected < len(v.filteredIdx)-1 {
							v.selected++
							v.updateOutput()
						}
					} else if x > leftWidth+1 {
						v.outputScroll += 3
						if v.outputScroll > len(v.currentOutputLines)-bodyHeight {
							v.outputScroll = len(v.currentOutputLines) - bodyHeight
						}
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				} else if btn == 0 { // Left click press
					r := y - 3
					if x <= leftWidth {
						v.activePane = PaneTimeline
						if idx := clickRowToIndex(r, v.timelineScroll, len(v.filteredIdx)); idx >= 0 {
							v.selected = idx
							v.updateOutput()
						}
					} else if x > leftWidth+1 {
						v.activePane = PaneOutput
					}
				}
			}
			continue
		}

		if v.inSearch {
			if len(key) == 1 {
				b := key[0]
				switch b {
				case 13, 10: // Enter
					v.inSearch = false
					if v.searchMode == SearchOutput {
						v.outputQuery = v.searchQuery
						v.updateMatches()
						bodyHeight := v.bodyHeight()
						if len(v.matchLines) > 0 {
							v.scrollToLine(v.matchLines[v.activeMatch], bodyHeight)
						}
					}
				case 27: // Esc
					v.inSearch = false
				case 127, 8: // Backspace
					if len(v.searchQuery) > 0 {
						v.searchQuery = v.searchQuery[:len(v.searchQuery)-1]
						if v.searchMode == SearchCommands {
							v.updateFilter()
							v.updateOutput()
							v.outputScroll = 0
						} else {
							v.outputQuery = v.searchQuery
							v.updateMatches()
							bodyHeight := v.bodyHeight()
							if len(v.matchLines) > 0 {
								v.scrollToLine(v.matchLines[v.activeMatch], bodyHeight)
							}
						}
					}
				case 3: // Ctrl+C
					return nil
				default:
					if b >= 32 && b < 127 {
						v.searchQuery += string(b)
						if v.searchMode == SearchCommands {
							v.updateFilter()
							v.updateOutput()
							v.outputScroll = 0
						} else {
							v.outputQuery = v.searchQuery
							v.updateMatches()
							bodyHeight := v.bodyHeight()
							if len(v.matchLines) > 0 {
								v.scrollToLine(v.matchLines[v.activeMatch], bodyHeight)
							}
						}
					}
				}
			}
		} else {
			// Normal mode navigation
			if len(key) == 1 {
				switch key[0] {
				case 'q', 27: // q or Esc
					return nil
				case 3: // Ctrl+C
					return nil
				case 9: // Tab
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
					if v.activePane == PaneTimeline {
						if v.selected < len(v.filteredIdx)-1 {
							v.selected++
							v.updateOutput()
							v.outputScroll = 0
						}
					} else {
						bodyHeight := v.bodyHeight()
						v.outputScroll += 3
						if v.outputScroll > len(v.currentOutputLines)-bodyHeight {
							v.outputScroll = len(v.currentOutputLines) - bodyHeight
						}
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				case 'k': // up
					if v.activePane == PaneTimeline {
						if v.selected > 0 {
							v.selected--
							v.updateOutput()
							v.outputScroll = 0
						}
					} else {
						v.outputScroll -= 3
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				case 4: // Ctrl+D
					if v.activePane == PaneOutput {
						bodyHeight := v.bodyHeight()
						v.outputScroll += bodyHeight
						if v.outputScroll > len(v.currentOutputLines)-bodyHeight {
							v.outputScroll = len(v.currentOutputLines) - bodyHeight
						}
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				case 21: // Ctrl+U
					if v.activePane == PaneOutput {
						bodyHeight := v.bodyHeight()
						v.outputScroll -= bodyHeight
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				case '/': // Search Commands mode
					v.inSearch = true
					v.searchMode = SearchCommands
					v.searchQuery = ""
					v.updateFilter()
					v.updateOutput()
					v.outputScroll = 0
				case 'f': // Search Output mode
					v.inSearch = true
					v.searchMode = SearchOutput
					v.searchQuery = ""
				case 'n': // Next output search match
					if len(v.matchLines) > 0 && v.activeMatch != -1 {
						v.activeMatch = (v.activeMatch + 1) % len(v.matchLines)
						bodyHeight := v.bodyHeight()
						v.scrollToLine(v.matchLines[v.activeMatch], bodyHeight)
					}
				case 'N': // Previous output search match
					if len(v.matchLines) > 0 && v.activeMatch != -1 {
						v.activeMatch = (v.activeMatch - 1 + len(v.matchLines)) % len(v.matchLines)
						bodyHeight := v.bodyHeight()
						v.scrollToLine(v.matchLines[v.activeMatch], bodyHeight)
					}
				}
			} else if len(key) == 3 && key[0] == 27 && key[1] == '[' {
				// Arrow keys: Escape sequences (e.g. Esc [ A)
				switch key[2] {
				case 'A': // Up arrow
					if v.activePane == PaneTimeline {
						if v.selected > 0 {
							v.selected--
							v.updateOutput()
							v.outputScroll = 0
						}
					} else {
						v.outputScroll -= 3
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				case 'B': // Down arrow
					if v.activePane == PaneTimeline {
						if v.selected < len(v.filteredIdx)-1 {
							v.selected++
							v.updateOutput()
							v.outputScroll = 0
						}
					} else {
						bodyHeight := v.bodyHeight()
						v.outputScroll += 3
						if v.outputScroll > len(v.currentOutputLines)-bodyHeight {
							v.outputScroll = len(v.currentOutputLines) - bodyHeight
						}
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				case 'C': // Right arrow
					v.activePane = PaneOutput
				case 'D': // Left arrow
					v.activePane = PaneTimeline
				}
			} else if len(key) == 4 && key[0] == 27 && key[1] == '[' && key[3] == '~' {
				// PageUp / PageDown keys: e.g. Esc [ 5 ~ (PageUp), Esc [ 6 ~ (PageDown)
				bodyHeight := v.bodyHeight()
				if key[2] == '5' { // PageUp
					if v.activePane == PaneOutput {
						v.outputScroll -= bodyHeight
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				} else if key[2] == '6' { // PageDown
					if v.activePane == PaneOutput {
						v.outputScroll += bodyHeight
						if v.outputScroll > len(v.currentOutputLines)-bodyHeight {
							v.outputScroll = len(v.currentOutputLines) - bodyHeight
						}
						if v.outputScroll < 0 {
							v.outputScroll = 0
						}
					}
				}
			}
		}
	}
}

func (v *Viewer) draw() {
	v.width, v.height, _ = term.GetSize(int(os.Stdin.Fd()))
	if v.width <= 0 || v.height <= 0 {
		v.width = 80
		v.height = 24
	}

	// Move cursor to home (don't clear screen to prevent flickering)
	var out bytes.Buffer
	out.WriteString("\x1b[H")

	// Calculate layouts
	// Left pane: 35% of width, min 30 chars
	leftWidth := (v.width * 35) / 100
	if leftWidth < 30 {
		leftWidth = 30
	}
	rightWidth := v.width - leftWidth - 1 // -1 for border
	bodyHeight := v.bodyHeight()
	v.timelineScroll = followSelection(v.selected, v.timelineScroll, bodyHeight)

	// Draw Header with active pane indicator
	paneStr := " PANE: [Timeline]  Output"
	if v.activePane == PaneOutput {
		paneStr = " PANE:  Timeline  [Output]"
	}
	header := fmt.Sprintf(" kuroko log viewer  [ File: %s ]%s", filepath.Base(v.logPath), paneStr)
	if n := textwidth.String(header); n > v.width {
		header = truncateDisplay(header, v.width)
	} else {
		header += strings.Repeat(" ", v.width-n)
	}
	out.WriteString(fmt.Sprintf("\x1b[30;47m%s\x1b[0m\x1b[K\r\n", header))

	// Draw Empty line between header and body
	out.WriteString("\x1b[K\r\n")

	// Draw Body split screen
	for r := 0; r < bodyHeight; r++ {
		// 1. Left pane (Timeline list of commands)
		var leftText string
		timelineRow := r + v.timelineScroll
		if timelineRow < len(v.filteredIdx) {
			cmdIdx := v.filteredIdx[timelineRow]
			cmd := v.allCmds[cmdIdx]

			// Format timestamp
			ts := ""
			if t, err := time.Parse(time.RFC3339, cmd.Timestamp); err == nil {
				ts = t.Format("15:04:05")
			}

			indicator := "  "
			if timelineRow == v.selected {
				indicator = "> "
			}

			leftText = fmt.Sprintf("%s[%s] %s", indicator, ts, cmd.Command)
			if n := textwidth.String(leftText); n > leftWidth {
				leftText = truncateDisplay(leftText, leftWidth)
			} else {
				leftText += strings.Repeat(" ", leftWidth-n)
			}

			if timelineRow == v.selected {
				// Highlight selected line in timeline
				leftText = fmt.Sprintf("\x1b[30;47m%s\x1b[0m", leftText)
			}
		} else {
			leftText = strings.Repeat(" ", leftWidth)
		}

		// 2. Middle Border (Highlight if Output pane is active)
		border := "|"
		if v.activePane == PaneOutput {
			border = "\x1b[33m|\x1b[0m"
		}

		// 3. Right pane (Output with scroll and query highlight)
		var rightText string
		if r+v.outputScroll < len(v.currentOutputLines) {
			line := v.currentOutputLines[r+v.outputScroll]
			line = strings.ReplaceAll(line, "\r", "")

			// Check if this line is in search matches
			isMatch := false
			matchIdx := -1
			for idx, ml := range v.matchLines {
				if ml == r+v.outputScroll {
					isMatch = true
					matchIdx = idx
					break
				}
			}

			truncated := line
			if n := textwidth.String(line); n > rightWidth {
				truncated = truncateDisplay(line, rightWidth)
			} else {
				truncated = line + strings.Repeat(" ", rightWidth-n)
			}

			if isMatch {
				isActive := (matchIdx == v.activeMatch)
				rightText = highlightQuery(truncated, v.outputQuery, isActive)
			} else {
				rightText = truncated
			}
		} else {
			rightText = strings.Repeat(" ", rightWidth)
		}

		out.WriteString(fmt.Sprintf("%s%s%s\x1b[K\r\n", leftText, border, rightText))
	}

	// Move cursor explicitly to the bottom line for footer to prevent screen scrolling
	out.WriteString(fmt.Sprintf("\x1b[%d;1H", v.height))

	// Draw Footer
	var footerText string
	if v.activePane == PaneTimeline {
		footerText = " [Tab/h/l]: Pane  [j/k/Arrows]: Move Command  [/]: Search Cmd  [f]: Find in Output  [q]: Quit"
	} else {
		if len(v.matchLines) > 0 {
			footerText = fmt.Sprintf(" [Tab/h/l]: Pane  [j/k/PgUpDown]: Scroll  [n/N]: Match %d/%d  [f]: Find in Output  [q]: Quit", v.activeMatch+1, len(v.matchLines))
		} else {
			footerText = " [Tab/h/l]: Pane  [j/k/PgUpDown]: Scroll  [f]: Find in Output  [q]: Quit"
		}
	}

	if v.inSearch {
		if v.searchMode == SearchCommands {
			footerText = fmt.Sprintf(" Search commands (Enter to confirm): %s_", v.searchQuery)
		} else {
			footerText = fmt.Sprintf(" Find in output (Enter to confirm): %s_", v.searchQuery)
		}
	}
	if n := textwidth.String(footerText); n > v.width {
		footerText = truncateDisplay(footerText, v.width)
	} else {
		footerText += strings.Repeat(" ", v.width-n)
	}
	out.WriteString(fmt.Sprintf("\x1b[30;47m%s\x1b[0m\x1b[K", footerText))

	os.Stdout.Write(out.Bytes())
}

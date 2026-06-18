package viewer

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/ryu/kuroko/internal/logger"
)

type CommandMetadata struct {
	Timestamp string `json:"timestamp"`
	Command   string `json:"command"`
	Offset    int64  `json:"offset"`
}

type Viewer struct {
	logPath     string
	logData     []byte
	allCmds     []CommandMetadata
	filteredIdx []int // Indices into allCmds matching current search
	selected    int   // Index into filteredIdx
	searchQuery string
	inSearch    bool
	width       int
	height      int
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
		logPath: logPath,
		logData: logData,
	}

	v.parseMetadata()
	v.updateFilter()
	return v, nil
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

	// Save screen, hide cursor, and clear screen on startup
	_, _ = os.Stdout.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J"))
	defer func() {
		_, _ = os.Stdout.Write([]byte("\x1b[?25h\x1b[?1049l"))
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
		if v.inSearch {
			if len(key) == 1 {
				b := key[0]
				switch b {
				case 13, 10: // Enter
					v.inSearch = false
				case 27: // Esc
					v.inSearch = false
				case 127, 8: // Backspace
					if len(v.searchQuery) > 0 {
						v.searchQuery = v.searchQuery[:len(v.searchQuery)-1]
						v.updateFilter()
					}
				case 3: // Ctrl+C
					return nil
				default:
					if b >= 32 && b < 127 {
						v.searchQuery += string(b)
						v.updateFilter()
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
				case 'j': // down
					if v.selected < len(v.filteredIdx)-1 {
						v.selected++
					}
				case 'k': // up
					if v.selected > 0 {
						v.selected--
					}
				case '/': // Search mode
					v.inSearch = true
					v.searchQuery = ""
					v.updateFilter()
				}
			} else if len(key) == 3 && key[0] == 27 && key[1] == '[' {
				// Arrow keys: Escape sequences (e.g. Esc [ A)
				switch key[2] {
				case 'A': // Up arrow
					if v.selected > 0 {
						v.selected--
					}
				case 'B': // Down arrow
					if v.selected < len(v.filteredIdx)-1 {
						v.selected++
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
	bodyHeight := v.height - 3            // header + empty line + footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	// Draw Header
	header := fmt.Sprintf(" kuroko log viewer  [ File: %s ]", filepath.Base(v.logPath))
	if len(header) < v.width {
		header += strings.Repeat(" ", v.width-len(header))
	}
	out.WriteString(fmt.Sprintf("\x1b[30;47m%s\x1b[0m\x1b[K\r\n", header[:v.width]))

	// Draw Empty line between header and body
	out.WriteString("\x1b[K\r\n")

	// Get command outputs
	var commandLines []string
	if len(v.filteredIdx) > 0 {
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
				commandLines = append(commandLines, rl)
			}
		}
	}

	// Draw Body split screen
	for r := 0; r < bodyHeight; r++ {
		// 1. Left pane (Timeline list of commands)
		var leftText string
		if r < len(v.filteredIdx) {
			cmdIdx := v.filteredIdx[r]
			cmd := v.allCmds[cmdIdx]

			// Format timestamp
			ts := ""
			if t, err := time.Parse(time.RFC3339, cmd.Timestamp); err == nil {
				ts = t.Format("15:04:05")
			}

			indicator := "  "
			if r == v.selected {
				indicator = "> "
			}

			leftText = fmt.Sprintf("%s[%s] %s", indicator, ts, cmd.Command)
			if len(leftText) > leftWidth {
				leftText = leftText[:leftWidth-3] + "..."
			} else {
				leftText += strings.Repeat(" ", leftWidth-len(leftText))
			}

			if r == v.selected {
				// Highlight selected line in timeline
				leftText = fmt.Sprintf("\x1b[30;47m%s\x1b[0m", leftText)
			}
		} else {
			leftText = strings.Repeat(" ", leftWidth)
		}

		// 2. Middle Border
		border := "|"

		// 3. Right pane (Output)
		var rightText string
		if r < len(commandLines) {
			line := commandLines[r]
			// Trim terminal spaces or strip double CR/LF residues if any
			line = strings.ReplaceAll(line, "\r", "")
			if len(line) > rightWidth {
				rightText = line[:rightWidth-3] + "..."
			} else {
				rightText = line + strings.Repeat(" ", rightWidth-len(line))
			}
		} else {
			rightText = strings.Repeat(" ", rightWidth)
		}

		out.WriteString(fmt.Sprintf("%s%s%s\x1b[K\r\n", leftText, border, rightText))
	}

	// Move cursor explicitly to the bottom line for footer to prevent screen scrolling
	out.WriteString(fmt.Sprintf("\x1b[%d;1H", v.height))

	// Draw Footer
	footerText := " [j/k/Arrows]: Navigate  [/]: Search  [q/Esc]: Quit"
	if v.inSearch {
		footerText = fmt.Sprintf(" Search query (Enter to confirm): %s_", v.searchQuery)
	}
	if len(footerText) < v.width {
		footerText += strings.Repeat(" ", v.width-len(footerText))
	}
	out.WriteString(fmt.Sprintf("\x1b[30;47m%s\x1b[0m\x1b[K", footerText[:v.width]))

	os.Stdout.Write(out.Bytes())
}

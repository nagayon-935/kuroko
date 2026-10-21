package viewer

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/ryu/kuroko/internal/logger"
	"github.com/ryu/kuroko/internal/logstore"
)

// chromeHeight is the number of terminal rows consumed by the header, the
// blank spacer line, and the footer, leaving the remainder for the body.
const chromeHeight = 3

type CommandMetadata struct {
	Timestamp    string `json:"timestamp"`
	Command      string `json:"command"`
	Offset       int64  `json:"offset"`
	WholeSession bool   `json:"-"`
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

type Viewer struct {
	statusMessage      string
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
	r, err := logstore.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	defer r.Close()
	logData, err := io.ReadAll(r)
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
	v.allCmds = append([]CommandMetadata{{Command: "Entire session", WholeSession: true}}, v.allCmds...)
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

	text, err := v.selectionText()
	if err == nil {
		v.currentOutputLines = strings.Split(string(text), "\n")
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
	runeIndex := 0
	// Use offsets into the original bytes: invalid UTF-8 consumes one byte
	// even though its decoded replacement rune would encode as three.
	for byteIndex := range line {
		byteOffsets[runeIndex] = byteIndex
		runeIndex++
	}
	byteOffsets[len(lineRunes)] = len(line)

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
					_, cmdBytes, _ := logger.SplitPromptInfo(lines[i+1])
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
		if query == "" || (!c.WholeSession && strings.Contains(strings.ToLower(c.Command), query)) {
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

func (v *Viewer) moveTimeline(delta int) {
	next := v.selected + delta
	if next >= 0 && next < len(v.filteredIdx) {
		v.selected = next
		v.updateOutput()
	}
}

func (v *Viewer) scrollOutput(delta int) {
	bodyHeight := v.bodyHeight()
	v.outputScroll = max(0, min(v.outputScroll+delta, max(0, len(v.currentOutputLines)-bodyHeight)))
}

func (v *Viewer) stepActivePane(timelineDelta, outputDelta int) {
	if v.activePane == PaneTimeline {
		v.moveTimeline(timelineDelta)
	} else {
		v.scrollOutput(outputDelta)
	}
}

func (v *Viewer) onSearchQueryChanged() {
	if v.searchMode == SearchCommands {
		v.updateFilter()
		v.updateOutput()
	} else {
		v.outputQuery = v.searchQuery
		v.updateMatches()
		if len(v.matchLines) > 0 {
			v.scrollToLine(v.matchLines[v.activeMatch], v.bodyHeight())
		}
	}
}

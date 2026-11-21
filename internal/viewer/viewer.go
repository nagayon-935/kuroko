package viewer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/nagayon-935/kuroko/internal/logger"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

// chromeHeight is the number of terminal rows consumed by the header, the
// blank spacer line, and the footer, leaving the remainder for the body.
const chromeHeight = 3

type CommandMetadata struct {
	Timestamp    string
	Command      string
	Offset       int64
	WholeSession bool
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
	statusMessage string
	logPath       string
	// logData is the whole log. It must never be mutated after loadSession:
	// currentOutputLines are zero-copy sub-slices that alias it.
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
	currentOutputLines [][]byte // sub-slices of logData (read-only)
	// highlight is the 1-based index into currentOutputLines of the line
	// RunAtLine pointed at (0 = none). It is cleared when the output changes.
	highlight int
	// revealHighlight asks the next draw to scroll the highlight into view,
	// because the terminal size is unknown until the first frame.
	revealHighlight bool
}

func Run(logPath string) error {
	v, err := newViewer(logPath)
	if err != nil {
		return err
	}
	return v.loop()
}

func newViewer(logPath string) (*Viewer, error) {
	v, err := loadSession(logPath)
	if err != nil {
		return nil, err
	}
	v.updateFilter()
	v.updateOutput()
	return v, nil
}

func loadSession(logPath string) (*Viewer, error) {
	r, err := logstore.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	defer r.Close()
	logData, err := readLog(r, logPath)
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
	return v, nil
}

// readLog reads the whole log. Uncompressed logs are read into a buffer
// preallocated from the file size, avoiding the doubling growth (and the
// transient ~2x memory) of io.ReadAll. If the file grows while reading,
// bytes.Buffer simply grows; if it shrinks, the shorter data is returned.
// Compressed logs (decided the same way as logstore.Open) are sized from the
// gzip trailer when it looks plausible (see gzipSizeHint), else just grow.
func readLog(r io.Reader, logPath string) ([]byte, error) {
	var buf bytes.Buffer
	if strings.HasSuffix(logPath, ".gz") {
		if n := gzipSizeHint(logPath); n > 0 {
			buf.Grow(n + bytes.MinRead)
		}
		if _, err := buf.ReadFrom(r); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	if info, err := os.Stat(logPath); err == nil && info.Size() > 0 {
		buf.Grow(int(info.Size()) + bytes.MinRead)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// gzipSizeHint returns the decompressed size recorded in the trailing ISIZE
// field of the gzip file at path, or 0 when it is unavailable or implausible.
// ISIZE is the size mod 2^32 of the last member only, so it is just a hint:
// it must be positive, at most maxDeflateRatio times the compressed size
// (deflate cannot expand data more than that) and at most 1 GiB.
// Any error means "no hint".
// maxDeflateRatio is deflate's theoretical maximum compression ratio
// (about 1032:1), so a larger ISIZE cannot belong to this file.
const maxDeflateRatio = 1032

func gzipSizeHint(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() < 4 {
		return 0
	}
	var tail [4]byte
	if _, err := f.ReadAt(tail[:], info.Size()-4); err != nil {
		return 0
	}
	n := int64(binary.LittleEndian.Uint32(tail[:]))
	if n <= 0 || n > maxDeflateRatio*info.Size() || n > 1<<30 {
		return 0
	}
	return int(n)
}

// outputLinePrefix marks metadata lines that are hidden from the output pane.
var outputLinePrefix = []byte("# kuroko:cmd:")

// splitOutputLines splits data on LF and drops lines starting with the
// metadata prefix, returning sub-slices of data (no copy).
//
// It is equivalent to strings.Split(string(filtered), "\n") where filtered is
// writeFiltered's output: that function emits the retained lines joined by LF
// (the separator is written lazily before each retained line; an input that
// ends in LF yields a final empty retained line; a fragment longer than the
// bufio buffer is a continuation of the same line, and the prefix test is made
// on the first fragment, which always holds the whole 13-byte prefix). Splitting
// the join of k retained lines yields exactly those k lines, except k == 0
// where the empty output splits into a single empty line.
func splitOutputLines(data []byte) [][]byte {
	lines := make([][]byte, 0, bytes.Count(data, []byte{'\n'})+1)
	for pos := 0; ; {
		rest := data[pos:]
		nl := bytes.IndexByte(rest, '\n')
		line := rest
		if nl >= 0 {
			line = rest[:nl]
		}
		if !bytes.HasPrefix(line, outputLinePrefix) {
			lines = append(lines, line)
		}
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	if len(lines) == 0 {
		lines = append(lines, data[:0])
	}
	return lines
}

func (v *Viewer) updateOutput() {
	v.currentOutputLines = nil
	v.matchLines = nil
	v.activeMatch = -1
	v.outputScroll = 0
	v.highlight = 0
	v.revealHighlight = false

	if len(v.filteredIdx) == 0 {
		return
	}

	if v.selected >= 0 && v.selected < len(v.filteredIdx) {
		if data, err := v.commandRange(v.filteredIdx[v.selected]); err == nil {
			v.currentOutputLines = splitOutputLines(data)
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
	asciiQuery := isASCII(v.outputQuery)
	for i, line := range v.currentOutputLines {
		if lineMatches(line, query, asciiQuery) {
			v.matchLines = append(v.matchLines, i)
		}
	}
	if len(v.matchLines) > 0 {
		v.activeMatch = 0
	} else {
		v.activeMatch = -1
	}
}

func isASCII[T string | []byte](s T) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// lineMatches reports strings.Contains(strings.ToLower(line), lowerQuery)
// without allocating when both line and the original query are pure ASCII
// (ASCII case folding then equals ToLower). Otherwise it falls back to the
// exact ToLower comparison, since ToLower can map non-ASCII runes (e.g. U+212A,
// U+0130) to ASCII ones.
func lineMatches(line []byte, lowerQuery string, asciiQuery bool) bool {
	if asciiQuery && isASCII(line) {
		return containsFoldASCII(line, lowerQuery)
	}
	return strings.Contains(strings.ToLower(string(line)), lowerQuery)
}

// containsFoldASCII reports whether ASCII-lowercased line contains lowerQuery,
// which must already be ASCII-lowercase.
func containsFoldASCII(line []byte, lowerQuery string) bool {
	n := len(lowerQuery)
	if n == 0 {
		return true
	}
	first := lowerQuery[0]
	for i := 0; i+n <= len(line); i++ {
		c := line[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != first {
			continue
		}
		j := 1
		for ; j < n; j++ {
			d := line[i+j]
			if 'A' <= d && d <= 'Z' {
				d += 'a' - 'A'
			}
			if d != lowerQuery[j] {
				break
			}
		}
		if j == n {
			return true
		}
	}
	return false
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
	data := v.logData

	// Scan line by line (LF-separated, including a final line without LF)
	// without materializing the lines; only metadata lines are converted.
	for pos := 0; ; {
		rest := data[pos:]
		nl := bytes.IndexByte(rest, '\n')
		line := rest
		if nl >= 0 {
			line = rest[:nl]
		}

		if bytes.HasPrefix(line, outputLinePrefix) {
			payload := string(bytes.TrimSuffix(line, []byte{'\r'})[len(outputLinePrefix):])

			var timestamp string
			var command string

			// For backwards compatibility, handle old format "timestamp|command"
			if idx := strings.Index(payload, "|"); idx >= 0 {
				timestamp = payload[:idx]
				command = payload[idx+1:]
			} else {
				timestamp = payload
				// Extract command from the next line (prompt + command). The
				// next line is intentionally not CR-trimmed here.
				if nl >= 0 {
					next := data[pos+nl+1:]
					if e := bytes.IndexByte(next, '\n'); e >= 0 {
						next = next[:e]
					}
					_, cmdBytes, _ := logger.SplitPromptInfo(next)
					if len(cmdBytes) > 0 {
						command = string(cmdBytes)
					} else {
						command = string(next)
					}
				}
			}

			command = strings.TrimSpace(strings.ReplaceAll(command, "\r", ""))

			cmds = append(cmds, CommandMetadata{
				Timestamp: timestamp,
				Command:   command,
				Offset:    int64(pos),
			})
		}
		if nl < 0 {
			break
		}
		pos += nl + 1
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

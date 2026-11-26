package viewer

import (
	"bytes"
	"fmt"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

// RunAtLine opens the viewer on the command whose output contains the 1-based
// raw log line (numbered as `kuroko grep` prints them), with the output pane
// focused and that line highlighted. A line outside the log is reported
// before the terminal is touched.
func RunAtLine(logPath string, line int) error {
	v, err := newViewerAtLine(logPath, line)
	if err != nil {
		return err
	}
	return v.loop()
}

func newViewerAtLine(logPath string, line int) (*Viewer, error) {
	v, err := newViewer(logPath)
	if err != nil {
		return nil, err
	}
	if err := v.focusRawLine(line); err != nil {
		return nil, err
	}
	return v, nil
}

// focusRawLine selects the command containing the raw line, switches to the
// output pane and marks the line. The scroll is deferred to the first draw,
// when the terminal size is known.
func (v *Viewer) focusRawLine(line int) error {
	cmdIdx, display, err := v.locateRawLine(line)
	if err != nil {
		return err
	}
	for i, idx := range v.filteredIdx {
		if idx == cmdIdx {
			v.selected = i
			break
		}
	}
	v.updateOutput()
	if len(v.currentOutputLines) > 0 {
		v.highlight = min(display, len(v.currentOutputLines)-1) + 1
		v.revealHighlight = true
	}
	v.activePane = PaneOutput
	return nil
}

// rawLineCount counts LF-delimited lines the way kuroko grep numbers them: a
// final line without LF counts, a trailing LF does not start another line.
func rawLineCount(data []byte) int {
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// locateRawLine maps a 1-based raw log line to the index in allCmds of the
// command whose output range contains it (0, the entire session, for the
// header, anything before the first command, and the footer) and to the index
// of the matching line in that selection's output lines.
//
// A line that is itself a "# kuroko:cmd:" metadata line has no output line of
// its own; it maps to the first output line of its command.
func (v *Viewer) locateRawLine(line int) (cmdIdx, display int, err error) {
	total := rawLineCount(v.logData)
	if line < 1 || line > total {
		return 0, 0, fmt.Errorf("line %d is out of range (the log has %d lines)", line, total)
	}
	off := 0
	for n := 1; n < line; n++ {
		off += bytes.IndexByte(v.logData[off:], '\n') + 1
	}

	cmdIdx = v.commandAt(int64(off))
	start, _, _ := v.rangeBounds(cmdIdx)
	// Count the retained (non-metadata) lines that begin before off. off is a
	// line start, so every counted line is LF-terminated.
	for pos := int(start); pos < off; {
		nl := bytes.IndexByte(v.logData[pos:], '\n')
		if !logstore.IsCommandMeta(v.logData[pos : pos+nl]) {
			display++
		}
		pos += nl + 1
	}
	return cmdIdx, display, nil
}

// commandAt returns the command (index into allCmds) whose output range
// contains the line starting at off, or 0 (the entire session) if none does.
func (v *Viewer) commandAt(off int64) int {
	for idx := 1; idx < len(v.allCmds); idx++ {
		if start, end, err := v.rangeBounds(idx); err == nil && start <= off && off < end {
			return idx
		}
	}
	// An empty line starting exactly at the footer's leading LF ends the last
	// command's text (that LF is excluded from its range, leaving a final
	// empty output line).
	for idx := len(v.allCmds) - 1; idx >= 1; idx-- {
		if start, end, err := v.rangeBounds(idx); err == nil && start < off && off == end {
			return idx
		}
	}
	return 0
}

package viewer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryu/kuroko/internal/logstore"
)

// selectionText uses the original log, so scrolling, filtering, truncation,
// and search highlighting cannot change the exported content.
func (v *Viewer) selectionText() ([]byte, error) {
	if v.selected < 0 || v.selected >= len(v.filteredIdx) {
		return nil, fmt.Errorf("no selected command or session")
	}
	idx := v.filteredIdx[v.selected]
	if idx < 0 || idx >= len(v.allCmds) {
		return nil, fmt.Errorf("invalid command selection")
	}
	cmd := v.allCmds[idx]
	start, end := cmd.Offset, int64(len(v.logData))
	if cmd.WholeSession {
		start = 0
	} else {
		end = int64(logstore.FooterOffset(v.logData))
		if idx+1 < len(v.allCmds) && v.allCmds[idx+1].Offset < end {
			end = v.allCmds[idx+1].Offset
		}
	}
	if start < 0 || start > end || end > int64(len(v.logData)) {
		return nil, fmt.Errorf("invalid command offsets")
	}
	lines := strings.Split(string(v.logData[start:end]), "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "# kuroko:cmd:") {
			filtered = append(filtered, line)
		}
	}
	return []byte(strings.Join(filtered, "\n")), nil
}

// Export writes a session (command=0) or a one-based command and its output.
// It supports both current and older metadata formats and compressed logs.
func Export(logPath string, command int, w io.Writer) error {
	v, err := newViewer(logPath)
	if err != nil {
		return err
	}
	if command < 0 || command >= len(v.allCmds) {
		return fmt.Errorf("command %d not found (this log has %d commands)", command, len(v.allCmds)-1)
	}
	v.selected = command
	data, err := v.selectionText()
	if err != nil {
		return err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

// ExportToFile never overwrites a log or an existing user file.
func ExportToFile(logPath string, command int, outputPath string) error {
	return writeNewExport(outputPath, func(w io.Writer) error { return Export(logPath, command, w) })
}

func writeNewExport(path string, write func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = errors.Join(write(f), f.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func (v *Viewer) exportSelection() {
	data, err := v.selectionText()
	if err != nil {
		v.statusMessage = "Export failed: " + err.Error()
		return
	}
	idx := v.filteredIdx[v.selected]
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(v.logPath), ".gz"), ".log")
	part := "session"
	if !v.allCmds[idx].WholeSession {
		part = fmt.Sprintf("command-%03d", idx)
	}
	name := base + "_" + part
	for suffix := 0; ; suffix++ {
		candidate := name + ".txt"
		if suffix > 0 {
			candidate = fmt.Sprintf("%s_%d.txt", name, suffix)
		}
		path, err := filepath.Abs(candidate)
		if err != nil {
			v.statusMessage = "Export failed: " + err.Error()
			return
		}
		err = writeNewExport(path, func(w io.Writer) error {
			n, err := w.Write(data)
			if err == nil && n != len(data) {
				return io.ErrShortWrite
			}
			return err
		})
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			v.statusMessage = "Export failed: " + err.Error()
			return
		}
		v.statusMessage = "Exported: " + path
		return
	}
}

package logstore

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const metadataWindow = 8192

// Open reads plain or gzip logs through the same interface.
func Open(path string) (io.ReadCloser, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular log file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".gz") {
		return f, nil
	}
	gr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("reading gzip: %w", err)
	}
	return &gzipLog{Reader: gr, file: f}, nil
}

type gzipLog struct {
	*gzip.Reader
	file *os.File
}

func (g *gzipLog) Close() error { return errors.Join(g.Reader.Close(), g.file.Close()) }

// headerComment returns the gzip header comment of the first member.
func (g *gzipLog) headerComment() string { return g.Header.Comment }

const (
	summaryCommentPrefix = "kuroko-summary ended="
	summaryCommentExit   = " exit="
	// maxSummaryComment bounds untrusted comment input; a valid comment is
	// well under 100 bytes.
	maxSummaryComment = 128
)

// parseSummaryComment parses the footer data stored in a gzip header comment:
//
//	kuroko-summary ended=<RFC3339Nano time> exit=<decimal exit code>
//
// The comment is untrusted input, so anything that deviates from the format
// yields ok=false and the caller falls back to reading the footer itself.
func parseSummaryComment(comment string) (ended time.Time, exit int, ok bool) {
	if len(comment) > maxSummaryComment {
		return time.Time{}, 0, false
	}
	rest, found := strings.CutPrefix(comment, summaryCommentPrefix)
	if !found {
		return time.Time{}, 0, false
	}
	endedStr, exitStr, found := strings.Cut(rest, summaryCommentExit)
	if !found {
		return time.Time{}, 0, false
	}
	ended, err := time.Parse(time.RFC3339Nano, endedStr)
	if err != nil {
		return time.Time{}, 0, false
	}
	exit, err = strconv.Atoi(exitStr)
	if err != nil || strconv.Itoa(exit) != exitStr {
		return time.Time{}, 0, false
	}
	return ended, exit, true
}

// Summary is read from the session header and footer, rather than from
// filesystem modification times, which change when logs are copied.
type Summary struct {
	Started  time.Time
	Ended    time.Time
	Command  string
	Target   string
	ExitCode *int
}

func (s Summary) Duration() time.Duration {
	if s.Started.IsZero() || s.Ended.Before(s.Started) {
		return 0
	}
	return s.Ended.Sub(s.Started)
}

func parseHeader(data []byte) Summary {
	var s Summary
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "# kuroko session log" {
		return s
	}
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "# ---") || line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "# Started":
			s.Started, _ = time.Parse(time.RFC3339, value)
		case "# Command":
			s.Command = value
		case "# Target":
			s.Target = value
		}
	}
	return s
}

// FooterOffset returns the start of a complete session footer, or len(data)
// for an unfinished/plain log. Body output that merely resembles metadata
// is not interpreted as a footer unless it terminates the log.
func FooterOffset(data []byte) int {
	marker := []byte("\n# " + strings.Repeat("-", 68) + "\n# Ended   : ")
	i := bytes.LastIndex(data, marker)
	if i < 0 {
		return len(data)
	}
	lines := strings.Split(strings.TrimSpace(string(data[i:])), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "# Exit    : ") {
		return len(data)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(lines[1], "# Ended   : "))); err != nil {
		return len(data)
	}
	if _, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lines[2], "# Exit    : "))); err != nil {
		return len(data)
	}
	return i
}

func ReadSummary(path string) (Summary, error) {
	r, err := Open(path)
	if err != nil {
		return Summary{}, err
	}
	defer r.Close()
	head, err := io.ReadAll(io.LimitReader(r, metadataWindow))
	if err != nil {
		return Summary{}, err
	}
	s := parseHeader(head)
	if g, isGzip := r.(*gzipLog); isGzip {
		if ended, code, ok := parseSummaryComment(g.headerComment()); ok {
			s.Ended = ended
			s.ExitCode = &code
			return s, nil
		}
	}
	tail := head
	if f, ok := r.(*os.File); ok {
		if len(head) == metadataWindow {
			info, err := f.Stat()
			if err != nil {
				return s, err
			}
			start := max(info.Size()-metadataWindow, 0)
			if _, err := f.Seek(start, io.SeekStart); err != nil {
				return s, err
			}
			tail, err = io.ReadAll(io.LimitReader(f, metadataWindow))
			if err != nil {
				return s, err
			}
		}
	} else {
		window := &tailWindow{data: append([]byte(nil), head...)}
		if _, err := io.Copy(window, r); err != nil {
			return s, err
		}
		tail = window.data
	}
	if i := FooterOffset(tail); i < len(tail) {
		lines := strings.Split(strings.TrimSpace(string(tail[i:])), "\n")
		s.Ended, _ = time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(lines[1], "# Ended   : ")))
		code, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lines[2], "# Exit    : ")))
		s.ExitCode = &code
	}
	return s, nil
}

type tailWindow struct{ data []byte }

func (w *tailWindow) Write(p []byte) (int, error) {
	n := len(p)
	if n >= metadataWindow {
		w.data = append(w.data[:0], p[n-metadataWindow:]...)
	} else {
		if excess := len(w.data) + n - metadataWindow; excess > 0 {
			copy(w.data, w.data[excess:])
			w.data = w.data[:len(w.data)-excess]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}

// LatestLogFile chooses by session start, falling back to mtime for logs
// without a kuroko header. Only the header is read, even for large gzip logs.
func LatestLogFile(dir string) (string, error) {
	entries, err := ListLogFiles(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no saved logs in %s", dir)
	}
	if err != nil {
		return "", err
	}
	var latest string
	var latestTime time.Time
	var latestModified time.Time
	for _, entry := range entries {
		path := entry.Path
		info, err := entry.Info()
		if err != nil {
			continue
		}
		r, err := Open(path)
		if err != nil {
			continue
		}
		head, readErr := io.ReadAll(io.LimitReader(r, metadataWindow))
		r.Close()
		if readErr != nil {
			continue
		}
		started := parseHeader(head).Started
		if started.IsZero() {
			started = info.ModTime()
		}
		modified := info.ModTime()
		if latest == "" || started.After(latestTime) || (started.Equal(latestTime) && (modified.After(latestModified) || modified.Equal(latestModified) && path > latest)) {
			latest, latestTime = path, started
			latestModified = modified
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no readable saved logs in %s", dir)
	}
	return latest, nil
}

// ResolveViewFile accepts either one log path or the convenience --last flag.
func ResolveViewFile(args []string, logDir func() (string, error)) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("usage: kuroko view <logfile> | kuroko view --last")
	}
	if args[0] == "--last" {
		dir, err := logDir()
		if err != nil {
			return "", err
		}
		return LatestLogFile(dir)
	}
	if strings.HasPrefix(args[0], "-") {
		return "", fmt.Errorf("unknown view option %q (use --last or an explicit file path)", args[0])
	}
	return ResolveLogFile(args[0], logDir)
}

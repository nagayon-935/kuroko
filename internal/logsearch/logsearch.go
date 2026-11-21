// Package logsearch greps across all saved kuroko logs, plain and gzip.
package logsearch

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

// MaxLineBytes caps how much of a single line is examined. Longer lines are
// deliberately truncated to this length (the remainder is skipped, not
// matched); the line still counts as one line for numbering.
const MaxLineBytes = 1 << 20

var metaPrefix = []byte("# kuroko:cmd:")

// Options tunes Search.
type Options struct {
	// FilesOnly prints only the relative path of each log with a match.
	FilesOnly bool
}

// Result summarises a Search.
type Result struct {
	// Matched is true if any log had a match.
	Matched bool
	// Skipped counts logs that could not be read and were skipped.
	Skipped int
	// Capped is set by Collect when more matches existed than its limit.
	Capped bool
}

// Search scans every log under dir for re and writes matches to out as
// <relative path>:<line>:<text>, in relative-path order. Logs that cannot be
// read are reported on warn and skipped. It reports whether anything matched and how many logs were skipped.
func Search(dir string, re *regexp.Regexp, opt Options, out, warn io.Writer) (Result, error) {
	return scanLogs(dir, warn, func(f logstore.LogFile) (bool, bool, error) {
		ok, err := searchFile(f, re, opt, out)
		return ok, false, err
	})
}

// Match is one matching line found by Collect.
type Match struct {
	// Path is the absolute path of the log.
	Path string
	// RelativePath is the path relative to the searched directory, as
	// Search prints it.
	RelativePath string
	// Line is the 1-based line number in the raw (decompressed) log.
	Line int
	// Text is the line as Search prints it (truncated to MaxLineBytes, with
	// the line terminator and one trailing CR removed).
	Text string
}

// Collect scans the logs exactly as Search does (same order, line numbering,
// truncation and metadata skipping) but returns the matches instead of
// printing them. A limit above zero stops the scan once more than limit
// matches exist: the first limit are returned and Result.Capped is set.
func Collect(dir string, re *regexp.Regexp, limit int, warn io.Writer) ([]Match, Result, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, Result{}, err
	}
	var matches []Match
	var capped bool
	res, err := scanLogs(absDir, warn, func(f logstore.LogFile) (bool, bool, error) {
		matched, err := scanFile(f.Path, re, func(n int, text []byte) (bool, error) {
			if limit > 0 && len(matches) >= limit {
				capped = true
				return true, nil
			}
			matches = append(matches, Match{Path: f.Path, RelativePath: f.RelativePath, Line: n, Text: string(text)})
			return false, nil
		})
		return matched, capped, err
	})
	res.Capped = capped
	return matches, res, err
}

// scanLogs runs scan over every log under dir in relative-path order. scan
// reports whether the log had a match and whether the whole search should
// stop. Unreadable logs are warned about and skipped; writeError aborts.
func scanLogs(dir string, warn io.Writer, scan func(logstore.LogFile) (matched, stop bool, err error)) (Result, error) {
	files, err := logstore.ListLogFiles(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, nil // no log directory yet means no logs
	}
	if err != nil {
		return Result{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })

	var res Result
	for _, f := range files {
		ok, stop, err := scan(f)
		if ok {
			res.Matched = true
		}
		var we writeError
		if errors.As(err, &we) {
			return res, err
		}
		if err != nil {
			res.Skipped++
			if _, werr := fmt.Fprintf(warn, "warning: skipping %s: %v\n", f.RelativePath, err); werr != nil {
				return res, fmt.Errorf("writing warning: %w", werr)
			}
		}
		if stop {
			break
		}
	}
	return res, nil
}

// writeError marks failures of the output writer, which must abort the search
// (unlike unreadable logs, which are skipped).
type writeError struct{ err error }

func (e writeError) Error() string { return "writing output: " + e.err.Error() }
func (e writeError) Unwrap() error { return e.err }

func searchFile(f logstore.LogFile, re *regexp.Regexp, opt Options, out io.Writer) (bool, error) {
	return scanFile(f.Path, re, func(n int, line []byte) (bool, error) {
		if opt.FilesOnly {
			if _, werr := fmt.Fprintln(out, f.RelativePath); werr != nil {
				return true, writeError{werr}
			}
			return true, nil
		}
		if _, werr := fmt.Fprintf(out, "%s:%d:%s\n", f.RelativePath, n, line); werr != nil {
			return true, writeError{werr}
		}
		return false, nil
	})
}

// scanFile calls visit for every non-metadata line of the log at path that
// matches re, with its 1-based number and text (a fresh slice the callee may
// keep). visit returns true to stop scanning this log; an error from it ends
// the scan and is returned as is. It reports whether any line matched.
func scanFile(path string, re *regexp.Regexp, visit func(n int, line []byte) (done bool, err error)) (bool, error) {
	r, err := logstore.Open(path)
	if err != nil {
		return false, err
	}
	defer r.Close()

	br := bufio.NewReaderSize(r, 64*1024)
	matched := false
	for n := 1; ; n++ {
		line, err := readLine(br)
		if len(line) > 0 || err == nil {
			if !bytes.HasPrefix(line, metaPrefix) && re.Match(line) {
				matched = true
				done, verr := visit(n, line)
				if verr != nil {
					return true, verr
				}
				if done {
					return true, nil
				}
			}
		}
		if err == io.EOF {
			return matched, nil
		}
		if err != nil {
			return matched, fmt.Errorf("reading: %w", err)
		}
	}
}

// readLine returns the next line without its newline (and without a trailing
// carriage return), truncated to MaxLineBytes. It returns io.EOF together with
// any final unterminated line.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if room := MaxLineBytes - len(line); room > 0 {
			line = append(line, chunk[:min(len(chunk), room)]...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		line = bytes.TrimSuffix(line, []byte("\n"))
		line = bytes.TrimSuffix(line, []byte("\r"))
		return line, err
	}
}

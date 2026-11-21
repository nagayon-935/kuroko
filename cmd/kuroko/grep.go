package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"

	"golang.org/x/term"

	"github.com/nagayon-935/kuroko/internal/logsearch"
	"github.com/nagayon-935/kuroko/internal/viewer"
)

// maxOpenMatches caps the list kuroko grep --open offers.
const maxOpenMatches = 10000

// Seams for tests: the terminal check and the viewers --open launches.
var (
	interactiveTerminal = func() bool {
		return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	}
	openAtLine = viewer.RunAtLine
	openPicker = viewer.RunPicker
)

const grepUsage = "usage: kuroko grep [-i] [-l | --open] <pattern>\nSearches all saved logs for a Go (RE2) regular expression.\n  -i  case-insensitive\n  -l  print only the names of logs with a match\n  --open  open the match in the viewer (a list to choose from when there are several)\nExit status: 0 if anything matched and nothing failed, 1 if nothing matched,\n2 on any error (including logs that could not be read, even if others matched).\n"

// Exit statuses of kuroko grep, following grep(1).
const (
	grepMatched   = 0
	grepNoMatch   = 1
	grepErrorExit = 2
)

// runGrep returns the exit status and, for failures not already reported on
// stderr (flag errors and unreadable-log warnings are), an error for the
// caller to print.
func runGrep(args []string, logDir string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, grepUsage) }
	var ignoreCase, filesOnly, open bool
	fs.BoolVar(&ignoreCase, "i", false, "")
	fs.BoolVar(&filesOnly, "l", false, "")
	fs.BoolVar(&open, "open", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return grepMatched, nil
		}
		// The flag package already printed the error and the usage.
		return grepErrorExit, nil
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return grepErrorExit, nil
	}
	if open && filesOnly {
		fs.Usage()
		return grepErrorExit, errors.New("--open cannot be combined with -l")
	}
	pattern := fs.Arg(0)
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return grepErrorExit, fmt.Errorf("invalid pattern: %w", err)
	}
	if open && !interactiveTerminal() {
		return grepErrorExit, errors.New("--open needs an interactive terminal")
	}
	dir, err := resolveLogDir(logDir)
	if err != nil {
		return grepErrorExit, err
	}
	if open {
		return grepOpen(dir, re, stderr)
	}
	res, err := logsearch.Search(dir, re, logsearch.Options{FilesOnly: filesOnly}, stdout, stderr)
	switch {
	case err != nil:
		return grepErrorExit, err
	case res.Skipped > 0:
		return grepErrorExit, nil // each skipped log was already warned about
	case !res.Matched:
		return grepNoMatch, nil
	}
	return grepMatched, nil
}

// grepOpen collects the matches instead of printing them and opens them in
// the viewer: directly for one match, through a picker for several. Exit
// statuses follow plain grep.
func grepOpen(dir string, re *regexp.Regexp, stderr io.Writer) (int, error) {
	matches, res, err := logsearch.Collect(dir, re, maxOpenMatches, stderr)
	if err != nil {
		return grepErrorExit, err
	}
	var openErr error
	switch len(matches) {
	case 0:
	case 1:
		openErr = openAtLine(matches[0].Path, matches[0].Line)
	default:
		openErr = openPicker(matches, res.Capped)
	}
	switch {
	case openErr != nil:
		return grepErrorExit, fmt.Errorf("opening viewer: %w", openErr)
	case res.Skipped > 0:
		return grepErrorExit, nil // each skipped log was already warned about
	case len(matches) == 0:
		return grepNoMatch, nil
	}
	return grepMatched, nil
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"

	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/logsearch"
)

const grepUsage = "usage: kuroko grep [-i] [-l] <pattern>\nSearches all saved logs for a Go (RE2) regular expression.\n  -i  case-insensitive\n  -l  print only the names of logs with a match\nExit status: 0 if anything matched and nothing failed, 1 if nothing matched,\n2 on any error (including logs that could not be read, even if others matched).\n"

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
	var ignoreCase, filesOnly bool
	fs.BoolVar(&ignoreCase, "i", false, "")
	fs.BoolVar(&filesOnly, "l", false, "")
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
	pattern := fs.Arg(0)
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return grepErrorExit, fmt.Errorf("invalid pattern: %w", err)
	}
	cfg, err := config.Resolve(config.Options{LogDir: logDir})
	if err != nil {
		return grepErrorExit, err
	}
	res, err := logsearch.Search(cfg.LogDir, re, logsearch.Options{FilesOnly: filesOnly}, stdout, stderr)
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

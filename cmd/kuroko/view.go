package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/nagayon-935/kuroko/internal/viewer"
)

const viewUsage = "usage: kuroko view [--follow | --line N] (--last | <logfile>)\nOpens a saved log in the split-pane viewer, or streams it with --follow (-f).\n  --last      open the most recent session\n  -f, --follow  stream the log to stdout until the session ends\n  --line N    open at 1-based raw log line N (cannot be combined with --follow)\n"

// runView implements `kuroko view`: --follow streams the log, --line opens
// the viewer at a line, otherwise the plain viewer runs. Argument and log
// resolution errors come back with inViewer false (main prints "view error");
// a failure of the viewer itself comes back with inViewer true ("viewer
// error").
func runView(args []string, logDir string, stdout, stderr io.Writer) (inViewer bool, err error) {
	if isHelpFlag(args) {
		fmt.Fprint(stderr, viewUsage)
		return false, nil
	}
	viewArgs, following := splitFollowFlag(args)
	if following {
		if _, _, hasLine, _ := splitLineFlag(viewArgs); hasLine {
			return false, errors.New("--line cannot be combined with --follow")
		}
		return false, runFollow(viewArgs, logDir, stdout)
	}
	viewArgs, line, hasLine, err := splitLineFlag(viewArgs)
	if err != nil {
		return false, err
	}
	path, err := resolveLogArgument(viewArgs, logDir)
	if err != nil {
		return false, err
	}
	if hasLine {
		return true, openAtLine(path, line)
	}
	return true, viewer.Run(path)
}

// splitFollowFlag removes every --follow/-f argument (before a "--"
// delimiter) and reports whether one was present. The remaining arguments are
// passed unchanged to resolveLogArgument, so view semantics stay the same.
func splitFollowFlag(args []string) (rest []string, found bool) {
	for i, arg := range args {
		if arg == "--" {
			return append(rest, args[i:]...), found
		}
		if arg == "--follow" || arg == "-f" {
			found = true
			continue
		}
		rest = append(rest, arg)
	}
	return rest, found
}

// splitLineFlag removes a --line N / --line=N argument (before a "--"
// delimiter) and returns its value. The remaining arguments are passed
// unchanged to resolveLogArgument. If --line is repeated the last one wins.
func splitLineFlag(args []string) (rest []string, line int, found bool, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(rest, args[i:]...), line, found, nil
		}
		var value string
		switch {
		case arg == "--line":
			if i+1 >= len(args) {
				return nil, 0, false, fmt.Errorf("--line requires a line number")
			}
			i++
			value = args[i]
		case strings.HasPrefix(arg, "--line="):
			value = strings.TrimPrefix(arg, "--line=")
		default:
			rest = append(rest, arg)
			continue
		}
		n, perr := strconv.Atoi(value)
		if perr != nil {
			return nil, 0, false, fmt.Errorf("--line needs a line number, got %q", value)
		}
		line, found = n, true
	}
	return rest, line, found, nil
}

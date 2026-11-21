package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/nagayon-935/kuroko/internal/follow"
)

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

// runFollow resolves the log named by args and streams it to stdout until the
// session ends or SIGINT/SIGTERM arrives (which is a clean exit).
func runFollow(args []string, logDir string, stdout io.Writer) error {
	path, err := resolveLogArgument(args, logDir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := follow.Follow(ctx, path, stdout, follow.Options{}); err != nil {
		return fmt.Errorf("following %s: %w", path, err)
	}
	return nil
}

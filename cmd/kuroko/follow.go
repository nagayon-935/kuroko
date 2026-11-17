package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
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

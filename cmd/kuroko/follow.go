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

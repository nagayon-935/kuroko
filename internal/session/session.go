package session

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logger"
	"github.com/ryu/kuroko/internal/notifier"
)

type Session struct {
	cfg      *config.Config
	args     []string
	log      *logger.Logger
	notifier notifier.Notifier
}

func New(cfg *config.Config, args []string) (*Session, error) {
	log, err := logger.New(cfg.LogDir, args, cfg.Redaction.Enabled)
	if err != nil {
		return nil, fmt.Errorf("creating log file: %w", err)
	}

	n := notifier.New(notifier.Config{
		Type:       cfg.Notifier.Type,
		WebhookURL: cfg.Notifier.WebhookURL,
	})

	return &Session{
		cfg:      cfg,
		args:     args,
		log:      log,
		notifier: n,
	}, nil
}

func (s *Session) Run() (int, error) {
	writeBanner(os.Stderr, s.args, s.cfg)

	fmt.Fprintf(os.Stderr, "\033[2m[kuroko] logging → %s\033[0m\n", s.log.Path)

	command := strings.Join(s.args, " ")

	if err := s.notifier.NotifyStart(command); err != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[kuroko] notify error: %v\033[0m\n", err)
	}

	start := time.Now()
	exitCode, err := s.runWithPTY()
	duration := time.Since(start)

	if cerr := s.log.Close(exitCode); cerr != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[kuroko] log close error: %v\033[0m\n", cerr)
	}

	logPath := s.log.Path
	if s.cfg.Storage.CompressOnClose {
		shouldCompress := true
		if s.cfg.Storage.CompressThresholdMB > 0 {
			if info, err := os.Stat(s.log.Path); err == nil {
				sizeMB := info.Size() / (1024 * 1024)
				shouldCompress = sizeMB >= int64(s.cfg.Storage.CompressThresholdMB)
			}
		}

		if shouldCompress {
			if compressedPath, cerr := logger.CompressFile(s.log.Path); cerr == nil {
				logPath = compressedPath
			} else {
				fmt.Fprintf(os.Stderr, "\033[33m[kuroko] compression error: %v\033[0m\n", cerr)
			}
		}
	}

	if s.cfg.Storage.Rotation.Enabled {
		// Run GC in background so we don't block terminal exit.
		go func() {
			if rerr := logger.RotateLogs(s.cfg.LogDir, s.cfg.Storage.Rotation.MaxAgeDays, s.cfg.Storage.Rotation.MaxTotalSizeMB); rerr != nil {
				fmt.Fprintf(os.Stderr, "\033[33m[kuroko] log rotation error: %v\033[0m\n", rerr)
			}
		}()
	}

	if nerr := s.notifier.NotifyEnd(logPath, command, exitCode, duration); nerr != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[kuroko] notify error: %v\033[0m\n", nerr)
	}

	return exitCode, err
}

func (s *Session) runWithPTY() (int, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return s.runPlain()
	}

	cmd := exec.Command(s.args[0], s.args[1:]...)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return 1, fmt.Errorf("pty start: %w", err)
	}
	defer ptmx.Close()

	stopResize := forwardResizeSignals(ptmx)
	defer stopResize()

	// Set raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return 1, fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	stopTermHandler := s.handleTerminationSignals(oldState)
	defer stopTermHandler()

	// stdin → PTY master. This goroutine intentionally outlives runWithPTY:
	// kuroko is a short-lived CLI process, so the blocked Stdin read is
	// reclaimed by the OS when the process exits — there is no long-running
	// server for it to leak into.
	go func() { io.Copy(ptmx, os.Stdin) }() //nolint:errcheck

	// PTY master → stdout + log file
	io.Copy(io.MultiWriter(os.Stdout, s.log), ptmx)

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 0, nil
	}
	return 0, nil
}

// forwardResizeSignals forwards terminal resize events (SIGWINCH) to the PTY
// for as long as the session runs. The returned stop function releases the
// signal channel and should be deferred by the caller.
func forwardResizeSignals(ptmx *os.File) (stop func()) {
	sigWinch := make(chan os.Signal, 1)
	signal.Notify(sigWinch, syscall.SIGWINCH)
	go func() {
		for range sigWinch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	sigWinch <- syscall.SIGWINCH
	return func() { signal.Stop(sigWinch) }
}

// handleTerminationSignals installs a SIGTERM/SIGHUP handler that restores
// the terminal and flushes the session log before the process exits.
// os.Exit skips deferred functions, so cleanup happens explicitly here.
// The returned stop function releases the signal channel and should be
// deferred by the caller.
func (s *Session) handleTerminationSignals(oldState *term.State) (stop func()) {
	sigTerm := make(chan os.Signal, 1)
	signal.Notify(sigTerm, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig, ok := <-sigTerm
		if !ok {
			return
		}
		os.Exit(s.terminateForSignal(sig, oldState))
	}()
	return func() {
		signal.Stop(sigTerm)
		close(sigTerm)
	}
}

// terminateForSignal restores the terminal and flushes the session log for
// termination signal sig, returning the process exit code to use. Split out
// from handleTerminationSignals so the cleanup logic can be unit tested
// without going through os.Exit.
func (s *Session) terminateForSignal(sig os.Signal, oldState *term.State) int {
	_ = term.Restore(int(os.Stdin.Fd()), oldState)
	code := 128
	if sysSig, ok := sig.(syscall.Signal); ok {
		code += int(sysSig)
	}
	if cerr := s.log.Close(code); cerr != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[kuroko] log close error: %v\033[0m\n", cerr)
	}
	return code
}

// runPlain is a non-PTY fallback for piped/scripted invocations.
func (s *Session) runPlain() (int, error) {
	cmd := exec.Command(s.args[0], s.args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, s.log)
	cmd.Stderr = io.MultiWriter(os.Stderr, s.log)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

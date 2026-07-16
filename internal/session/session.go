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
	cmd      *exec.Cmd // set once the child process starts; read by terminateForSignal to propagate termination signals
	start    time.Time // set at the beginning of Run(); read by terminateForSignal to compute NotifyEnd's duration
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

	s.start = time.Now()
	exitCode, err := s.runWithPTY()
	duration := time.Since(s.start)

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
	s.cmd = cmd

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

	// PTY master → stdout + log file. The log side is wrapped fail-open so a
	// log write error (e.g. disk full) never stalls this copy: an unwrapped
	// MultiWriter aborts entirely on the first error/short write from any
	// writer, which would stop draining the PTY and hang the child process
	// (and this terminal) the next time it writes.
	io.Copy(io.MultiWriter(os.Stdout, newFailOpenWriter(s.log)), ptmx)

	return waitResult(cmd.Wait())
}

// waitResult converts the error returned by cmd.Wait() (or the error from
// cmd.Run(), which has the same shape) into a process exit code. Split out
// so the exit-code mapping is unit-testable without a real PTY or process.
func waitResult(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 1, fmt.Errorf("waiting for command: %w", err)
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
	return func() {
		signal.Stop(sigWinch)
		close(sigWinch)
	}
}

// handleTerminationSignals installs a SIGTERM/SIGHUP handler that restores
// the terminal and flushes the session log before the process exits.
// os.Exit skips deferred functions, so cleanup happens explicitly here.
// The returned stop function releases the signal channel and should be
// deferred by the caller.
func (s *Session) handleTerminationSignals(oldState *term.State) (stop func()) {
	sigTerm := make(chan os.Signal, 1)
	signal.Notify(sigTerm, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
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

	sysSig, hasSysSig := sig.(syscall.Signal)
	if s.cmd != nil && s.cmd.Process != nil && hasSysSig {
		_ = s.cmd.Process.Signal(sysSig)
	}

	code := 128
	if hasSysSig {
		code += int(sysSig)
	}
	if cerr := s.log.Close(code); cerr != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[kuroko] log close error: %v\033[0m\n", cerr)
	}

	command := strings.Join(s.args, " ")
	if nerr := s.notifier.NotifyEnd(s.log.Path, command, code, time.Since(s.start)); nerr != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[kuroko] notify error: %v\033[0m\n", nerr)
	}

	return code
}

// runPlain is a non-PTY fallback for piped/scripted invocations.
func (s *Session) runPlain() (int, error) {
	cmd := exec.Command(s.args[0], s.args[1:]...)
	cmd.Stdin = os.Stdin
	// Each stream gets its own fail-open wrapper around the shared log: cmd
	// copies Stdout and Stderr concurrently in separate goroutines, and a
	// hard failure on one stream must not affect the other's independent
	// fail-open state.
	cmd.Stdout = io.MultiWriter(os.Stdout, newFailOpenWriter(s.log))
	cmd.Stderr = io.MultiWriter(os.Stderr, newFailOpenWriter(s.log))

	return waitResult(cmd.Run())
}

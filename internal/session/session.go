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

	s.log.Close(exitCode)

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
			_ = logger.RotateLogs(s.cfg.LogDir, s.cfg.Storage.Rotation.MaxAgeDays, s.cfg.Storage.Rotation.MaxTotalSizeMB)
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

	// Forward terminal resize events to the PTY.
	sigWinch := make(chan os.Signal, 1)
	signal.Notify(sigWinch, syscall.SIGWINCH)
	defer signal.Stop(sigWinch)
	go func() {
		for range sigWinch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	sigWinch <- syscall.SIGWINCH

	// Set raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return 1, fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Handle SIGTERM/SIGHUP: restore terminal and flush log before the process
	// dies. os.Exit skips defers, so we do it explicitly in the goroutine.
	sigTerm := make(chan os.Signal, 1)
	signal.Notify(sigTerm, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig, ok := <-sigTerm
		if !ok {
			return
		}
		term.Restore(int(os.Stdin.Fd()), oldState)
		code := 128
		if s, ok := sig.(syscall.Signal); ok {
			code += int(s)
		}
		s.log.Close(code)
		os.Exit(code)
	}()
	defer func() {
		signal.Stop(sigTerm)
		close(sigTerm)
	}()

	// stdin → PTY master
	go func() { io.Copy(ptmx, os.Stdin) }()

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

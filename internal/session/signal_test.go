package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logger"
	"github.com/ryu/kuroko/internal/notifier"
)

// newTestSession creates a real Session (with a real Logger backed by a
// temp-dir log file) so terminateForSignal can be exercised end to end.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	cfg := &config.Config{LogDir: t.TempDir()}
	s, err := New(cfg, []string{"echo", "signal_test"})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return s
}

func TestTerminateForSignalExitCode(t *testing.T) {
	tests := []struct {
		name string
		sig  os.Signal
		want int
	}{
		{"SIGTERM", syscall.SIGTERM, 128 + int(syscall.SIGTERM)},
		{"SIGHUP", syscall.SIGHUP, 128 + int(syscall.SIGHUP)},
		{"SIGINT", syscall.SIGINT, 128 + int(syscall.SIGINT)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession(t)
			// A zero-value State is a valid, non-nil pointer; term.Restore
			// on a non-tty fd (as under `go test`) fails harmlessly and the
			// error is intentionally ignored, matching production behavior.
			got := s.terminateForSignal(tt.sig, &term.State{})
			if got != tt.want {
				t.Errorf("terminateForSignal(%v) = %d; want %d", tt.sig, got, tt.want)
			}
		})
	}
}

func TestTerminateForSignalClosesLog(t *testing.T) {
	s := newTestSession(t)
	logPath := s.log.Path

	code := s.terminateForSignal(syscall.SIGTERM, &term.State{})
	if code != 128+int(syscall.SIGTERM) {
		t.Fatalf("unexpected exit code %d", code)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	footer := "# Exit    : " + strconv.Itoa(code)
	if !strings.Contains(string(data), footer) {
		t.Errorf("log file missing exit footer %q; got:\n%s", footer, data)
	}

	// A second Close (mirroring the normal-exit path racing the signal
	// handler) must be a no-op: it must not panic and must return the same
	// result as the first call, proving the sync.Once guard works end to
	// end through terminateForSignal.
	if err := s.log.Close(999); err != nil {
		t.Errorf("second Close() returned error: %v; want nil (idempotent)", err)
	}
}

func TestForwardResizeSignalsStop(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no PTY available in this environment: %v", err)
	}
	defer tty.Close()
	defer ptmx.Close()

	stop := forwardResizeSignals(ptmx)
	// Calling stop must release the signal channel without panicking or
	// blocking, even though the internal goroutine may still be parked on
	// the (now-unregistered) channel.
	stop()
}

// TestForwardResizeSignalsStopReleasesGoroutine proves stop() actually lets
// the internal "for range sigWinch" goroutine exit (by closing the channel),
// rather than just detaching it from the OS signal package and leaking it
// forever. Uses a bounded poll on runtime.NumGoroutine() to avoid flakiness
// from unrelated goroutines settling.
func TestForwardResizeSignalsStopReleasesGoroutine(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no PTY available in this environment: %v", err)
	}
	defer tty.Close()
	defer ptmx.Close()

	before := runtime.NumGoroutine()
	stop := forwardResizeSignals(ptmx)
	stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutine count did not return to baseline after stop(): before=%d after=%d", before, runtime.NumGoroutine())
}

// spyNotifier records NotifyEnd calls so terminateForSignal's notification
// behavior can be asserted without a real Discord/Slack webhook.
type spyNotifier struct {
	endCalls    int
	lastCode    int
	lastCommand string
}

func (s *spyNotifier) NotifyStart(string) error { return nil }

func (s *spyNotifier) NotifyEnd(logPath, command string, exitCode int, duration time.Duration) error {
	s.endCalls++
	s.lastCode = exitCode
	s.lastCommand = command
	return nil
}

func TestTerminateForSignalNotifiesEnd(t *testing.T) {
	cfg := &config.Config{LogDir: t.TempDir()}
	log, err := logger.New(cfg.LogDir, []string{"echo", "notify_test"}, false)
	if err != nil {
		t.Fatalf("logger.New(): %v", err)
	}
	spy := &spyNotifier{}
	s := &Session{cfg: cfg, args: []string{"echo", "notify_test"}, log: log, notifier: spy, start: time.Now()}

	code := s.terminateForSignal(syscall.SIGTERM, &term.State{})

	if spy.endCalls != 1 {
		t.Fatalf("NotifyEnd called %d times; want 1", spy.endCalls)
	}
	if spy.lastCode != code {
		t.Errorf("NotifyEnd exitCode = %d; want %d", spy.lastCode, code)
	}
	if spy.lastCommand != "echo notify_test" {
		t.Errorf("NotifyEnd command = %q; want %q", spy.lastCommand, "echo notify_test")
	}
}

// TestTerminateForSignalPropagatesToChild proves the child process actually
// receives the terminating signal (not just the parent's own terminal
// cleanup). "sleep" has no custom signal handling, so its default
// disposition for SIGTERM (immediate termination by that signal) gives a
// deterministic, shell-trap-timing-independent way to verify delivery via
// the OS wait status.
func TestTerminateForSignalPropagatesToChild(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}

	cfg := &config.Config{LogDir: t.TempDir()}
	log, err := logger.New(cfg.LogDir, []string{"sleep", "5"}, false)
	if err != nil {
		t.Fatalf("logger.New(): %v", err)
	}
	s := &Session{
		cfg:      cfg,
		args:     []string{"sleep", "5"},
		log:      log,
		notifier: notifier.New(notifier.Config{}),
		cmd:      cmd,
		start:    time.Now(),
	}

	s.terminateForSignal(syscall.SIGTERM, &term.State{})

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case waitErr := <-waitDone:
		exitErr, ok := waitErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("child Wait() error = %v (%T); want *exec.ExitError", waitErr, waitErr)
		}
		ws, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok {
			t.Fatalf("Sys() = %T; want syscall.WaitStatus", exitErr.Sys())
		}
		if !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
			t.Errorf("child wait status = %+v; want signaled by SIGTERM (propagation failed)", ws)
		}
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("child did not exit after terminateForSignal (signal not propagated)")
	}
}

// TestHelperProcessSIGINT is not a real test; it's a subprocess entry point
// invoked (via re-exec of the test binary) by
// TestHandleTerminationSignalsReactsToSIGINT. It installs the real
// termination-signal handler and blocks so the parent test can verify SIGINT
// is registered end to end, including the os.Exit call, which cannot be
// exercised in-process without killing the test binary itself.
func TestHelperProcessSIGINT(t *testing.T) {
	if os.Getenv("KUROKO_SIGINT_HELPER") != "1" {
		t.Skip("not running as SIGINT helper subprocess")
	}
	cfg := &config.Config{LogDir: os.Getenv("KUROKO_SIGINT_LOGDIR")}
	s, err := New(cfg, []string{"echo", "sigint_helper"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper New() error:", err)
		os.Exit(2)
	}
	stop := s.handleTerminationSignals(&term.State{})
	defer stop()
	select {} // block until SIGINT arrives and the handler calls os.Exit
}

func TestHandleTerminationSignalsReactsToSIGINT(t *testing.T) {
	logDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessSIGINT")
	cmd.Env = append(os.Environ(),
		"KUROKO_SIGINT_HELPER=1",
		"KUROKO_SIGINT_LOGDIR="+logDir,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting helper process: %v", err)
	}

	// Give the helper a moment to install the signal handler.
	time.Sleep(200 * time.Millisecond)

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("sending SIGINT: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	wantCode := 128 + int(syscall.SIGINT)

	select {
	case waitErr := <-waitDone:
		exitErr, ok := waitErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("helper Wait() error = %v (%T); want *exec.ExitError", waitErr, waitErr)
		}
		if code := exitErr.ExitCode(); code != wantCode {
			t.Errorf("helper exit code = %d; want %d", code, wantCode)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("helper process did not exit after SIGINT within 5s (SIGINT not handled)")
	}

	entries, err := os.ReadDir(logDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a log file in %s; ReadDir err=%v entries=%v", logDir, err, entries)
	}
	data, err := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	footer := fmt.Sprintf("# Exit    : %d", wantCode)
	if !strings.Contains(string(data), footer) {
		t.Errorf("log missing exit footer %q; got:\n%s", footer, data)
	}
}

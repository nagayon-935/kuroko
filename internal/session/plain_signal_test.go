package session

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

// TestHelperProcessPlainSession is not a real test; it is the subprocess
// entry point of TestPlainSessionWritesFooterOnTermination. It runs a whole
// non-PTY session (stdin is not a terminal) so the parent can signal it.
func TestHelperProcessPlainSession(t *testing.T) {
	if os.Getenv("KUROKO_PLAIN_HELPER") != "1" {
		t.Skip("not running as plain-session helper subprocess")
	}
	cfg := &config.Config{LogDir: os.Getenv("KUROKO_PLAIN_LOGDIR")}
	s, err := New(cfg, []string{"sh", "-c", "echo ready; sleep 30"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper New() error:", err)
		os.Exit(2)
	}
	code, _ := s.Run()
	os.Exit(code)
}

func TestPlainSessionWritesFooterOnTermination(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			// Arrange: a running non-PTY session.
			logDir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessPlainSession")
			cmd.Env = append(os.Environ(), "KUROKO_PLAIN_HELPER=1", "KUROKO_PLAIN_LOGDIR="+logDir)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("starting helper process: %v", err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready := make(chan bool, 1)
			go func() {
				sc := bufio.NewScanner(stdout)
				for sc.Scan() {
					if sc.Text() == "ready" {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("helper exited before the wrapped command started")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("wrapped command did not start within 10s")
			}
			// The handler is installed right after the command starts.
			time.Sleep(200 * time.Millisecond)

			// Act
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatalf("sending %v: %v", sig, err)
			}
			waitDone := make(chan error, 1)
			go func() { waitDone <- cmd.Wait() }()

			// Assert
			wantCode := 128 + int(sig)
			select {
			case waitErr := <-waitDone:
				var exitErr *exec.ExitError
				if !errors.As(waitErr, &exitErr) {
					t.Fatalf("helper Wait() error = %v; want *exec.ExitError", waitErr)
				}
				if code := exitErr.ExitCode(); code != wantCode {
					t.Errorf("helper exit code = %d; want %d (killed by default signal action?)", code, wantCode)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("helper did not exit within 5s of %v", sig)
			}
			entries, err := logstore.ListLogFiles(logDir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("want one log in %s; err=%v entries=%v", logDir, err, entries)
			}
			data, err := os.ReadFile(entries[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			if footer := fmt.Sprintf("# Exit    : %d", wantCode); !strings.Contains(string(data), footer) {
				t.Errorf("log missing footer %q:\n%s", footer, data)
			}
		})
	}
}

// slowStartNotifier blocks NotifyStart until released.
type slowStartNotifier struct {
	release chan struct{}
	started chan struct{}
	err     error
	ended   bool
}

func (n *slowStartNotifier) NotifyStart(string) error {
	close(n.started)
	<-n.release
	return n.err
}

func (n *slowStartNotifier) NotifyEnd(string, string, int, time.Duration) error {
	n.ended = true
	return nil
}

func TestRunDoesNotWaitForStartNotificationBeforeCommand(t *testing.T) {
	// Arrange: a webhook that never answers until the command has finished.
	cfg := &config.Config{LogDir: t.TempDir()}
	marker := filepath.Join(t.TempDir(), "command-ran")
	s, err := New(cfg, []string{"touch", marker})
	if err != nil {
		t.Fatal(err)
	}
	n := &slowStartNotifier{release: make(chan struct{}), started: make(chan struct{})}
	s.notifier = n

	// Act: Run must get through the command and reach finishLog, where it
	// waits for the pending notification.
	done := make(chan int, 1)
	go func() {
		code, _ := s.Run()
		done <- code
	}()
	<-n.started
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not run while the start notification was still pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("Run returned before the start notification completed; the request would be cut off at exit")
	case <-time.After(100 * time.Millisecond):
	}
	close(n.release)

	// Assert
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d; want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the start notification completed")
	}
	if !n.ended {
		t.Error("NotifyEnd was not called")
	}
}

func TestWaitResultReportsSignalDeathAs128PlusSignal(t *testing.T) {
	// Arrange: a child that dies of SIGTERM.
	err := exec.Command("sh", "-c", "kill -TERM $$").Run()

	// Act
	code, gotErr := waitResult(err)

	// Assert
	if gotErr != nil {
		t.Fatalf("waitResult error = %v; want nil", gotErr)
	}
	if want := 128 + int(syscall.SIGTERM); code != want {
		t.Errorf("exit code = %d; want %d (not -1, which os.Exit turns into 255)", code, want)
	}
}

func TestRunReportsTheHandledSignalWhenTheNormalPathFinishesFirst(t *testing.T) {
	// Arrange: the signal handler has published its code, but the child
	// (which trapped the signal) exits 0 and Run's normal path reaches
	// finish before the handler does.
	cfg := &config.Config{LogDir: t.TempDir()}
	s, err := New(cfg, []string{"true"})
	if err != nil {
		t.Fatal(err)
	}
	want := 128 + int(syscall.SIGTERM)
	s.signalExit.Store(int32(want))

	// Act
	code, err := s.Run()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if code != want {
		t.Errorf("Run() = %d; want the handled signal's %d", code, want)
	}
	data, err := os.ReadFile(s.log.Path)
	if err != nil {
		t.Fatal(err)
	}
	if footer := fmt.Sprintf("# Exit    : %d", want); !strings.Contains(string(data), footer) {
		t.Errorf("log footer does not record the signal; want %q in:\n%s", footer, data)
	}
}

func TestTerminateForSignalKeepsTheFirstSignal(t *testing.T) {
	s := newTestSession(t)

	first := s.terminateForSignal(syscall.SIGTERM, nil)
	_ = s.terminateForSignal(syscall.SIGHUP, nil)

	if got := int(s.signalExit.Load()); got != first {
		t.Errorf("signalExit = %d; want the first signal's %d", got, first)
	}
}

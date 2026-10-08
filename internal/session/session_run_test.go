package session

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryu/kuroko/internal/config"
)

func TestWaitResultNilError(t *testing.T) {
	code, err := waitResult(nil)
	if err != nil {
		t.Errorf("err = %v; want nil", err)
	}
	if code != 0 {
		t.Errorf("code = %d; want 0", code)
	}
}

type fileCheckingNotifier struct {
	endCalls int
	path     string
	err      error
}

func (n *fileCheckingNotifier) NotifyStart(string) error { return nil }

func (n *fileCheckingNotifier) NotifyEnd(path, command string, code int, duration time.Duration) error {
	n.endCalls++
	n.path = path
	_, n.err = os.Stat(path)
	return n.err
}

func TestFinishNotifiesBeforeRotation(t *testing.T) {
	cfg := &config.Config{
		LogDir: t.TempDir(),
		Storage: config.StorageConfig{Rotation: config.RotationConfig{
			Enabled: true, MaxTotalSizeMB: 1,
		}},
	}
	s, err := New(cfg, []string{"bash"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.log.Close(0) })
	// A sparse log larger than the size cap will be removed by rotation.
	if err := os.Truncate(s.log.Path, 2*1024*1024); err != nil {
		t.Fatal(err)
	}
	n := &fileCheckingNotifier{}
	s.notifier = n
	s.start = time.Now()
	s.finish(0)
	if n.endCalls != 1 || n.err != nil {
		t.Fatalf("notification must read log before rotation: calls=%d, err=%v", n.endCalls, n.err)
	}
	if _, err := os.Stat(s.log.Path); !os.IsNotExist(err) {
		t.Fatalf("rotation did not finish: %v", err)
	}
}

func TestFinishConcurrentCallsCompressAndNotifyOnce(t *testing.T) {
	cfg := &config.Config{
		LogDir:  t.TempDir(),
		Storage: config.StorageConfig{CompressOnClose: true},
	}
	s, err := New(cfg, []string{"bash"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.log.Close(0) })
	n := &fileCheckingNotifier{}
	s.notifier = n
	s.start = time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.finish(0)
		}()
	}
	wg.Wait()
	if n.endCalls != 1 || n.err != nil || !strings.HasSuffix(n.path, ".log.gz") {
		t.Fatalf("completed log notification: calls=%d, path=%s, err=%v", n.endCalls, n.path, n.err)
	}
	if _, err := os.Stat(s.log.Path); !os.IsNotExist(err) {
		t.Fatalf("uncompressed source still exists: %v", err)
	}
}

func TestWaitResultExitError(t *testing.T) {
	// "false" always exits 1, giving a real *exec.ExitError deterministically.
	waitErr := exec.Command("false").Run()

	code, err := waitResult(waitErr)
	if err != nil {
		t.Errorf("err = %v; want nil (ExitError must not be surfaced as a Go error)", err)
	}
	if code != 1 {
		t.Errorf("code = %d; want 1", code)
	}
}

func TestWaitResultNonExitError(t *testing.T) {
	// A generic, non-ExitError failure (here: a plain sentinel) must not be
	// silently swallowed as a successful exit.
	sentinel := errors.New("wait: i/o error")

	code, err := waitResult(sentinel)
	if err == nil {
		t.Fatal("err = nil; want non-nil for a non-ExitError Wait() failure")
	}
	if code != 1 {
		t.Errorf("code = %d; want 1", code)
	}
}

// TestRunPlainSurvivesLogWriteFailure simulates the log destination failing
// mid-session (e.g. disk full) by closing the log's underlying file before
// Run() writes to it. Before the fail-open fix, a write error from
// io.MultiWriter would propagate out of cmd.Run() as a non-ExitError and
// runPlain would report a spurious failure (or, for a PTY session producing
// enough output to fill the pipe buffer, hang indefinitely). This test
// documents the fix at the runPlain path, which is what `go test` actually
// exercises since os.Stdin is not a TTY under the test harness.
func TestRunPlainSurvivesLogWriteFailure(t *testing.T) {
	cfg := &config.Config{LogDir: t.TempDir()}
	s, err := New(cfg, []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	// Force the log destination closed so any subsequent write fails.
	if err := s.log.Close(0); err != nil {
		t.Fatalf("pre-closing log: %v", err)
	}

	code, err := s.Run()
	if err != nil {
		t.Fatalf("Run() error = %v; want nil (fail-open must swallow the log write failure)", err)
	}
	if code != 0 {
		t.Errorf("code = %d; want 0 (echo's own exit code, unaffected by the log failure)", code)
	}
}

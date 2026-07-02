package session

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/ryu/kuroko/internal/config"
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

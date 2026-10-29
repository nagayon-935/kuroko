package session

import (
	"io"
	"sync"
	"sync/atomic"

	"github.com/nagayon-935/kuroko/internal/console"
)

// failOpenWriter wraps an io.Writer so that a write error (or short write)
// is reported once to stderr and then treated as a permanent no-op success.
// This exists because runWithPTY/runPlain tee output through
// io.MultiWriter(stdout, log): MultiWriter aborts the whole copy on the
// first error or short write from any writer, which would stall the PTY
// drain and hang the foreground terminal if the log destination fails
// (e.g. disk full). PTY transparency to the terminal must never depend on
// the log succeeding.
type failOpenWriter struct {
	w         io.Writer
	warnOnce  sync.Once
	failed    atomic.Bool
	onFailure func()
}

func newFailOpenWriter(w io.Writer) *failOpenWriter {
	return &failOpenWriter{w: w}
}

func (f *failOpenWriter) Write(p []byte) (int, error) {
	if !f.failed.Load() {
		n, err := f.w.Write(p)
		if err != nil || n != len(p) {
			f.failed.Store(true)
			f.warnOnce.Do(func() {
				if f.onFailure != nil {
					f.onFailure()
				}
				if err == nil {
					err = io.ErrShortWrite
				}
				console.Warnf("log write error: %v (logging disabled for remainder of session)", err)
			})
		}
	}
	// Always report a full, error-free write so io.MultiWriter keeps
	// draining the other writer(s) regardless of this one's health.
	return len(p), nil
}

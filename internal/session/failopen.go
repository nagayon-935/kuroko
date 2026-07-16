package session

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
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
	w        io.Writer
	warnOnce sync.Once
	failed   atomic.Bool
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
				if err == nil {
					err = io.ErrShortWrite
				}
				fmt.Fprintf(os.Stderr, "\033[33m[kuroko] log write error: %v (logging disabled for remainder of session)\033[0m\n", err)
			})
		}
	}
	// Always report a full, error-free write so io.MultiWriter keeps
	// draining the other writer(s) regardless of this one's health.
	return len(p), nil
}

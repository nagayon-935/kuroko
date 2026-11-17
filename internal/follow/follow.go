// Package follow streams a kuroko session log to a writer like `tail -f`,
// stopping once the session footer shows the log is complete.
package follow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

// DefaultInterval is how often the file is polled for appended bytes.
const DefaultInterval = 200 * time.Millisecond

// tailWindow bounds how much of the stream is retained for footer detection.
// The footer is three short lines; this matches logstore's metadata window.
const tailWindow = 8192

// Options tunes Follow.
type Options struct {
	// Interval is the polling interval; DefaultInterval when <= 0.
	Interval time.Duration
}

// Follow writes the log at path to w from the beginning, then keeps writing
// appended bytes until one of these end conditions, all with a nil error:
//   - the complete session footer has been seen (the footer is written in one
//     piece and ends with a newline, so a footer without its final newline is
//     still being written and is waited for);
//   - ctx is cancelled (also while catching up on a large log);
//   - the plain file disappeared (for example replaced by <name>.gz), the path
//     now names a different file, or the file was truncated. For the first two
//     the still-open file is drained one last time first.
//
// Gzip logs are always complete and are copied once.
func Follow(ctx context.Context, path string, w io.Writer, opt Options) error {
	interval := opt.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	if strings.HasSuffix(path, ".gz") {
		return copyAll(path, w)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}
	defer f.Close()

	d := &drainer{ctx: ctx, f: f, w: w, buf: make([]byte, 32*1024)}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		// Drain everything currently available before deciding anything.
		if err := d.drain(); err != nil || ctx.Err() != nil {
			return err
		}
		if d.footerComplete() {
			return nil
		}
		done, err := d.gone(path)
		if err != nil || done {
			return err
		}
		timer.Reset(interval)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

// drainer copies a file to w while remembering what it has read.
type drainer struct {
	ctx   context.Context
	f     *os.File
	w     io.Writer
	buf   []byte
	tail  []byte // last tailWindow bytes, for footer detection
	total int64  // bytes read so far
}

// drain copies bytes to EOF. It stops early, without error, when ctx is
// cancelled; callers check ctx.Err() afterwards.
func (d *drainer) drain() error {
	for d.ctx.Err() == nil {
		n, rerr := d.f.Read(d.buf)
		if n > 0 {
			if _, werr := d.w.Write(d.buf[:n]); werr != nil {
				return fmt.Errorf("writing output: %w", werr)
			}
			d.total += int64(n)
			d.tail = appendTail(d.tail, d.buf[:n])
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("reading log: %w", rerr)
		}
	}
	return nil
}

// footerComplete reports whether the stream ends with a whole footer. The
// footer's last line is "# Exit    : <n>\n"; without the newline the exit
// code may still be missing digits.
func (d *drainer) footerComplete() bool {
	return logstore.FooterOffset(d.tail) < len(d.tail) && bytes.HasSuffix(d.tail, []byte("\n"))
}

// gone reports whether following should end because the file at path was
// removed, replaced or truncated, draining the open file once more where
// bytes may still be unread.
func (d *drainer) gone(path string) (bool, error) {
	fi, err := d.f.Stat()
	if err != nil {
		return false, fmt.Errorf("checking log: %w", err)
	}
	if fi.Size() < d.total {
		return true, nil // truncated: the offset points past the new end
	}
	pi, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist) || (err == nil && !os.SameFile(fi, pi)):
		return true, d.drain()
	case err != nil:
		return false, fmt.Errorf("checking log path: %w", err)
	}
	return false, nil
}

// appendTail appends p to tail, keeping only the last tailWindow bytes.
func appendTail(tail, p []byte) []byte {
	tail = append(tail, p...)
	if len(tail) > tailWindow {
		tail = append(tail[:0], tail[len(tail)-tailWindow:]...)
	}
	return tail
}

func copyAll(path string, w io.Writer) error {
	r, err := logstore.Open(path)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}
	defer r.Close()
	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("copying log: %w", err)
	}
	return nil
}

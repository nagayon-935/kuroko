package logger

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

// Logger writes session output to a file as human-readable plain text.
//
// Output flows through a short pipeline: Write carries escape sequences split
// across calls and gates full-screen (alternate screen) output, then the
// terminal emulator turns what remains into rows — editing the current row
// (lineBuffer), classifying prompts (promptTracker), masking credentials
// (redactor) — and holds recent rows where cursor movement can still rewrite
// them (rowWindow) before they reach the file.
type Logger struct {
	mu           sync.Mutex // guards writes, terminal state, and closure
	file         *os.File
	buf          *bufio.Writer // sits between the terminal and file; flushed at the end of every Write
	rawFile      *os.File      // non-nil when KUROKO_RAW_DEBUG=1; receives raw PTY bytes
	Path         string
	writeSeq     uint64    // monotonic counter for raw debug chunks
	seqBuf       []byte    // incomplete escape sequence carried across Write calls
	mergeBuf     []byte    // scratch buffer used only when seqBuf must be prepended
	normBuf      []byte    // scratch buffer for CRLF normalization
	altScreen    bool      // true while a full-screen app (vim/less) owns the terminal
	logAltScreen bool      // screen wraps the whole connection in alternate-screen mode
	term         *terminal // turns the byte stream into log rows
	closed       bool
	closeErr     error
	writeErr     error
}

// writeBufferSize is the size of the buffer between the row window and the
// log file. Rows are small, so writing each one straight to the file costs a
// syscall per line; the buffer is flushed after every Write, which keeps the
// file as current as before.
const writeBufferSize = 64 << 10

func New(logDir string, args []string, redactionEnabled bool) (*Logger, error) {
	if info, err := os.Stat(logDir); err != nil || !info.IsDir() {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("not a log directory: %s", logDir)
	}
	dir, err := logstore.EnsureSubdir(logDir, LogSubdir(args))
	if err != nil {
		return nil, err
	}
	filename := generateFilename(args)
	f, err := createLogFile(dir, filename)
	if err != nil {
		return nil, err
	}
	path := f.Name()

	cmdLine := sanitizeHeaderValue(strings.Join(args, " "), redactionEnabled)
	targetAddr := sanitizeHeaderValue(TargetAddress(args), redactionEnabled)

	header := logstore.FormatHeader(time.Now(), cmdLine, targetAddr)
	if _, err := f.WriteString(header); err != nil {
		f.Close()
		_ = os.Remove(path)
		return nil, err
	}

	buf := bufio.NewWriterSize(f, writeBufferSize)
	l := &Logger{
		file:         f,
		buf:          buf,
		Path:         path,
		logAltScreen: len(args) > 0 && filepath.Base(args[0]) == "screen",
		term:         newTerminal(buf, redactionEnabled, isNetworkSessionCommand(args)),
	}

	// The raw debug file holds the PTY stream before redaction, so it is
	// never written while redaction is enabled.
	if os.Getenv("KUROKO_RAW_DEBUG") == "1" && !redactionEnabled {
		rawPath := path + ".raw"
		rf, err := os.OpenFile(rawPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			l.rawFile = rf
		}
	}

	return l, nil
}

var headerNewlineReplacer = strings.NewReplacer("\r", " ", "\n", " ")

func sanitizeHeaderValue(s string, redact bool) string {
	s = headerNewlineReplacer.Replace(s)
	if redact {
		s = string(RedactText([]byte(s)))
	}
	return s
}

func (l *Logger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, os.ErrClosed
	}

	if l.rawFile != nil {
		l.writeSeq++
		fmt.Fprintf(l.rawFile, "=== write %d (%d bytes) ===\n%q\n", l.writeSeq, len(p), p)
	}

	// Merge buffered partial escape sequence from the previous write only when
	// one exists, avoiding a full-chunk copy on the common path.
	data := p
	if len(l.seqBuf) > 0 {
		l.mergeBuf = append(append(l.mergeBuf[:0], l.seqBuf...), p...)
		l.seqBuf = l.seqBuf[:0]
		data = l.mergeBuf
	}

	// Save any trailing incomplete ANSI escape sequence for the next write.
	if n := incompleteEscapePrefix(data); n > 0 {
		l.seqBuf = append(l.seqBuf[:0], data[len(data)-n:]...)
		data = data[:len(data)-n]
	}

	// Flush even after a failed write so rows that did reach the buffer are
	// not held back; the first error wins.
	err := l.writeFiltered(data)
	if ferr := l.buf.Flush(); err == nil {
		err = ferr
	}
	if err != nil && l.writeErr == nil {
		l.writeErr = err
	}
	return len(p), err
}

// writeFiltered handles alternate-screen suppression.
// Full-screen apps (vim, less, htop) bracket their output with
// \x1b[?1049h / \x1b[?1047h / \x1b[?47h (enter) and the matching ...l (exit).  While in alternate-screen mode the raw
// PTY stream produces garbled content, so we suppress it. When wrapping
// screen itself, this mode contains the connection's output and must be logged.
func (l *Logger) writeFiltered(data []byte) error {
	for len(data) > 0 {
		if !l.altScreen {
			idx, n := indexAny(data, enterAltScreen)
			if idx < 0 {
				return l.feedNormalized(data)
			}
			if err := l.feedNormalized(data[:idx]); err != nil {
				return err
			}
			// Consume the transition; screen sessions keep the output inside it.
			l.altScreen = true
			data = data[idx+n:]
		} else {
			idx, n := indexAny(data, exitAltScreen)
			if idx < 0 {
				if l.logAltScreen {
					return l.feedNormalized(data)
				}
				return nil // still inside alternate screen; discard
			}
			if l.logAltScreen {
				if err := l.feedNormalized(data[:idx]); err != nil {
					return err
				}
			}
			l.altScreen = false
			data = data[idx+n:]
		}
	}
	return nil
}

func (l *Logger) feedNormalized(data []byte) error {
	if bytes.IndexByte(data, '\r') < 0 {
		return l.term.feed(data)
	}
	l.normBuf = normalizeLineEndingsInto(l.normBuf[:0], data)
	return l.term.feed(l.normBuf)
}

// Close flushes any remaining buffered output, writes the session footer, and
// closes the underlying files. It is safe to call concurrently or more than
// once (e.g. from both the normal exit path and a SIGTERM/SIGHUP handler
// racing to shut down before the process dies) — only the first call does
// the work; later calls return the same result.
func (l *Logger) Close(exitCode int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		l.closeErr = errors.Join(l.writeErr, l.doClose(exitCode))
	}
	return l.closeErr
}

func (l *Logger) doClose(exitCode int) (err error) {
	// Release both files even when flushing the buffered output fails.
	defer func() {
		if err != nil {
			// Best effort: get whatever is buffered into the file. The
			// failure that brought us here is already in err.
			_ = l.buf.Flush()
		}
		err = errors.Join(err, l.file.Close())
		if l.rawFile != nil {
			err = errors.Join(err, l.rawFile.Close())
			l.rawFile = nil
		}
	}()

	// Output held back as a possibly split escape sequence will not be
	// completed any more; pass it on so trailing text is not lost.
	if len(l.seqBuf) > 0 {
		seq := l.seqBuf
		l.seqBuf = nil
		if err := l.writeFiltered(seq); err != nil {
			return err
		}
	}
	// The session is ending, so no further redraw can occur: write out every
	// row still held for one, including an unterminated last line.
	if err := l.term.finish(); err != nil {
		return err
	}

	footer := logstore.FormatFooter(time.Now(), exitCode)
	if _, err := l.buf.WriteString(footer); err != nil {
		return err
	}
	if err := l.buf.Flush(); err != nil {
		return err
	}
	// Make the finished log durable before reporting it saved: closing the
	// file alone leaves the data in the page cache until the kernel writes
	// it back, so a power loss right after the session could still lose it.
	return l.file.Sync()
}

// SetScreenRows tells the logger how many rows the user's terminal shows.
// Cursor movement cannot reach rows that have scrolled off the top of the
// screen, so only that many recent rows stay in memory for a redraw to
// rewrite; older ones go to the file right away. This bounds what a crash
// can lose to about one screen. Non-positive heights are ignored.
func (l *Logger) SetScreenRows(rows int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	err := l.term.rows.setLimit(rows)
	if ferr := l.buf.Flush(); err == nil {
		err = ferr
	}
	if err != nil && l.writeErr == nil {
		l.writeErr = err
	}
}

// InAltScreen returns true if a full-screen application (like vim) is active.
func (l *Logger) InAltScreen() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.altScreen
}

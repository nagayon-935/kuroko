package logger

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ryu/kuroko/internal/logstore"
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
	rawFile      *os.File // non-nil when KUROKO_RAW_DEBUG=1; receives raw PTY bytes
	Path         string
	writeSeq     uint64    // monotonic counter for raw debug chunks
	seqBuf       []byte    // incomplete escape sequence carried across Write calls
	altScreen    bool      // true while a full-screen app (vim/less) owns the terminal
	logAltScreen bool      // screen wraps the whole connection in alternate-screen mode
	term         *terminal // turns the byte stream into log rows
	closed       bool
	closeErr     error
	writeErr     error
}

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

	header := fmt.Sprintf(
		"# kuroko session log\n# Started : %s\n# Command : %s\n# Target  : %s\n# %s\n\n",
		time.Now().Format(time.RFC3339Nano),
		strings.Join(args, " "),
		TargetAddress(args),
		strings.Repeat("-", 68),
	)
	if _, err := f.WriteString(header); err != nil {
		f.Close()
		return nil, err
	}

	l := &Logger{
		file:         f,
		Path:         path,
		logAltScreen: len(args) > 0 && filepath.Base(args[0]) == "screen",
		term:         newTerminal(f, redactionEnabled, isNetworkSessionCommand(args)),
	}

	// The raw debug file holds the PTY stream before redaction, so it is
	// never written while redaction is enabled.
	if os.Getenv("KUROKO_RAW_DEBUG") == "1" && !redactionEnabled {
		rawPath := path + ".raw"
		rf, err := os.OpenFile(rawPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err == nil {
			l.rawFile = rf
		}
	}

	return l, nil
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

	// Merge buffered partial escape sequence from the previous write so we
	// never split an escape sequence across buffer boundaries.
	data := append(l.seqBuf, p...)
	l.seqBuf = nil

	// Save any trailing incomplete ANSI escape sequence for the next write.
	if n := incompleteEscapePrefix(data); n > 0 {
		tail := data[len(data)-n:]
		l.seqBuf = make([]byte, n)
		copy(l.seqBuf, tail)
		data = data[:len(data)-n]
	}

	err := l.writeFiltered(data)
	if err != nil && l.writeErr == nil {
		l.writeErr = err
	}
	return len(p), err
}

// writeFiltered handles alternate-screen suppression.
// Full-screen apps (vim, less, htop) bracket their output with \x1b[?1049h
// (enter) and \x1b[?1049l (exit).  While in alternate-screen mode the raw
// PTY stream produces garbled content, so we suppress it. When wrapping
// screen itself, this mode contains the connection's output and must be logged.
func (l *Logger) writeFiltered(data []byte) error {
	for len(data) > 0 {
		if !l.altScreen {
			idx := bytes.Index(data, enterAltScreen)
			if idx < 0 {
				return l.term.feed(normalizeLineEndings(data))
			}
			if err := l.term.feed(normalizeLineEndings(data[:idx])); err != nil {
				return err
			}
			// Consume the transition; screen sessions keep the output inside it.
			l.altScreen = true
			data = data[idx+len(enterAltScreen):]
		} else {
			idx := bytes.Index(data, exitAltScreen)
			if idx < 0 {
				if l.logAltScreen {
					return l.term.feed(normalizeLineEndings(data))
				}
				return nil // still inside alternate screen; discard
			}
			if l.logAltScreen {
				if err := l.term.feed(normalizeLineEndings(data[:idx])); err != nil {
					return err
				}
			}
			l.altScreen = false
			data = data[idx+len(exitAltScreen):]
		}
	}
	return nil
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
		err = errors.Join(err, l.file.Close())
		if l.rawFile != nil {
			err = errors.Join(err, l.rawFile.Close())
			l.rawFile = nil
		}
	}()

	// The session is ending, so no further redraw can occur: write out every
	// row still held for one, including an unterminated last line.
	if err := l.term.finish(); err != nil {
		return err
	}

	footer := fmt.Sprintf(
		"\n# %s\n# Ended   : %s\n# Exit    : %d\n",
		strings.Repeat("-", 68),
		time.Now().Format(time.RFC3339Nano),
		exitCode,
	)
	_, err = l.file.WriteString(footer)
	return err
}

// InAltScreen returns true if a full-screen application (like vim) is active.
func (l *Logger) InAltScreen() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.altScreen
}

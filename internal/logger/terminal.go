package logger

import (
	"fmt"
	"io"
	"time"
	"unicode/utf8"
)

// terminal is a minimal terminal emulator that turns the PTY byte stream
// into log rows. Control characters and CSI sequences edit the current row
// (lineBuffer) or move between rows still held in memory (rowWindow); each
// newline commits the row, tagged as a command when promptTracker finds a
// prompt on it, and masked by the redactor when redaction is enabled.
type terminal struct {
	line     lineBuffer
	rows     rowWindow
	prompts  promptTracker
	redactor *redactor // nil when redaction is disabled

	pendingCR bool   // \r was the last control char seen; defer reset until next char
	inEsc     bool   // true if currently parsing an ANSI escape sequence
	escBuf    []byte // buffer for the current escape sequence
	runeBuf   []byte // incomplete UTF-8 rune carried across feed calls
}

func newTerminal(out io.Writer, redactionEnabled, networkMode bool) *terminal {
	t := &terminal{
		rows:    rowWindow{out: out},
		prompts: promptTracker{networkMode: networkMode},
	}
	if redactionEnabled {
		t.redactor = &redactor{}
	}
	return t
}

// feed runs data through the terminal simulation. It handles the control
// characters that readline uses for in-place line editing:
//   - \r  (0x0D) carriage return — set pendingCR; the reset to column 0 is
//     deferred until the next printable character arrives.
//   - \b  (0x08) backspace — move cursor one column left
//   - \n  (0x0A) newline    — commit the current line
//
// pendingCR is the key to handling the PTY driver's ONLCR behaviour: the
// driver converts \r\n → \r\r\n, and that sequence is often split across
// two successive write calls (\r at the end of one, \n at the start of the
// next).  By NOT resetting the cursor immediately on \r, the content built
// up in the line survives until the \n commits it.  If a printable char
// follows \r instead (readline overwrite), the deferred reset fires then.
//
// The prompt snapshot handles the readline "accept-line" redraw: just before
// executing a command, bash/readline erases the prompt and redraws only the
// bare command text (e.g. "cat foo" instead of "user@host:~$ cat foo").  The
// first bare \r snapshots the full prompt+command; if the subsequent
// overwrite produces fewer chars, the snapshot is committed so the prompt is
// preserved.
func (t *terminal) feed(data []byte) error {
	for _, b := range data {
		if t.inEsc {
			t.escBuf = append(t.escBuf, b)
			if isCompleteEscape(t.escBuf) || len(t.escBuf) > 64 {
				t.inEsc = false
				if len(t.escBuf) <= 64 {
					t.handleEscape(t.escBuf)
				}
			}
			continue
		}
		if b == 0x1b {
			if t.pendingCR {
				t.snapshotLine()
			}
			t.inEsc = true
			t.escBuf = []byte{b}
			continue
		}

		switch b {
		case '\n':
			// If pendingCR is set the \r was the CR half of a CRLF pair —
			// do not reset the cursor; commit the content that is already there.
			t.pendingCR = false
			if err := t.commitLine(); err != nil {
				return err
			}
		case '\r':
			t.pendingCR = true
		case '\b':
			if t.pendingCR {
				t.line.home()
				t.pendingCR = false
			}
			t.line.backspace()
		default:
			if b < 0x20 {
				continue // discard other non-printable control bytes
			}
			// Accumulate UTF-8 bytes until a full rune is available; a
			// multi-byte character (e.g. Japanese) may be split across
			// several PTY bytes, or even across Write() calls, since this
			// state persists between calls.
			t.runeBuf = append(t.runeBuf, b)
			if !utf8.FullRune(t.runeBuf) {
				continue
			}
			r, _ := utf8.DecodeRune(t.runeBuf)
			t.runeBuf = t.runeBuf[:0]

			if t.pendingCR {
				t.snapshotLine()
			}
			t.line.put(r)
		}
	}
	return nil
}

// commitLine records the current row in the row window, tagged with a
// command marker when it carries a prompt, and starts a fresh row.
func (t *terminal) commitLine() error {
	out, hasCommand := t.prompts.classify(t.line.text())

	var meta []byte
	if hasCommand {
		meta = []byte(fmt.Sprintf("# kuroko:cmd:%s\n", time.Now().Format(time.RFC3339)))
	}

	if t.redactor != nil {
		out = t.redactor.redact(out)
	}
	if err := t.rows.commit(pendingRow{meta: meta, text: out}); err != nil {
		return err
	}
	t.line.reset()
	t.prompts.lineCommitted()
	return nil
}

// snapshotLine hands the row, up to the cursor, to the prompt tracker and
// then performs the carriage return deferred by pendingCR. It runs on the
// first escape or printable byte to arrive after a bare \r, just before that
// byte overwrites the row — see feed for why this snapshot is needed to
// survive readline's accept-line redraw.
func (t *terminal) snapshotLine() {
	if t.line.col > 0 {
		t.prompts.snapshot(t.line.text())
	}
	t.line.home()
	t.pendingCR = false
}

// handleEscape applies the CSI sequences that affect what the log records;
// everything else (colors, OSC titles, two-char codes, …) is dropped.
func (t *terminal) handleEscape(seq []byte) {
	if len(seq) < 3 || seq[1] != '[' {
		return // Ignore non-CSI sequences (like OSC title or two-char codes)
	}

	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])
	n := csiCount(params)

	switch final {
	case 'K': // Clear line from cursor
		switch params {
		case "", "0":
			t.line.eraseToEnd()
		case "2":
			t.line.reset()
		}
	case 'C': // Cursor Forward (Right)
		t.line.forward(n)
	case 'D': // Cursor Backward (Left)
		t.line.backward(n)
	case 'A': // Cursor Up
		t.rows.up(n)
	case 'B': // Cursor Down
		t.rows.down(n)
	case 'J': // Erase in Display
		switch params {
		case "", "0":
			t.rows.eraseBelow()
		case "2", "3":
			t.rows.eraseAll()
		}
	case 'P': // Delete character(s) at cursor
		t.line.deleteChars(n)
	}
}

// finish writes out every row still held in memory, including an
// unterminated last row, once the session has ended and no further redraw
// can occur.
func (t *terminal) finish() error {
	if err := t.rows.flush(); err != nil {
		return err
	}

	// Flush any unterminated line; apply the same prompt recovery as commitLine.
	out := t.prompts.finalLine(t.line.text())
	if len(out) == 0 {
		return nil
	}
	if t.redactor != nil {
		out = t.redactor.redact(out)
	}
	_, err := t.rows.out.Write(append(out, '\n'))
	return err
}

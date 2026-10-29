package logger

import "io"

// maxPendingRows bounds the in-memory window of committed-but-not-yet-flushed
// rows (see rowWindow). Ink-style CLIs (Claude Code, Codex, Google's
// Antigravity CLI, …) redraw a live region — a spinner, a bordered box, a
// streaming panel — by moving the cursor up N lines and reprinting, without
// ever entering the alternate screen (so vim/less-style suppression doesn't
// apply). A generous window comfortably covers any realistic live-redraw
// region while bounding memory for pathological input.
const maxPendingRows = 500

// pendingRow is one committed terminal row buffered in memory, not yet
// written to disk, so that a subsequent cursor-up + reprint can overwrite it
// in place instead of the redraw being permanently duplicated as new lines.
type pendingRow struct {
	meta []byte // optional "# kuroko:cmd:...\n" metadata line preceding text; nil if none
	text []byte // the row's rendered content, without trailing newline
}

// rowWindow holds the most recently committed rows in memory, where cursor
// movement can still rewrite them, and writes rows to out once they scroll
// out of reach.
type rowWindow struct {
	out    io.Writer
	rows   []pendingRow
	cursor int // index into rows the next commit lands on; == len(rows) means "new row"
}

// commit places row at the current cursor position within the window: if
// the cursor was moved up via CSI A (cursor points at an already-buffered
// row), it overwrites that row in place — the redraw simply replaces the
// stale frame instead of duplicating it — otherwise it appends a new row.
// Rows that scroll off the top of the window (beyond maxPendingRows) are
// written out in order, since a live-redraw region is never that tall in
// practice.
func (w *rowWindow) commit(row pendingRow) error {
	if w.cursor < len(w.rows) {
		w.rows[w.cursor] = row
	} else {
		w.rows = append(w.rows, row)
	}
	w.cursor++
	for len(w.rows) > maxPendingRows {
		if err := w.write(w.rows[0]); err != nil {
			return err
		}
		w.rows = w.rows[1:]
		w.cursor--
	}
	return nil
}

// up moves the cursor up n rows (CSI A) so the next commit overwrites a
// previously committed, still-pending row (Ink-style redraw) instead of
// appending a duplicate line.
func (w *rowWindow) up(n int) { w.cursor = max(w.cursor-n, 0) }

// down moves the cursor down n rows (CSI B), at most to the row after the
// last pending one.
func (w *rowWindow) down(n int) { w.cursor = min(w.cursor+n, len(w.rows)) }

// eraseBelow drops the pending rows from the cursor down (CSI J / 0J),
// which a redraw is about to overwrite.
func (w *rowWindow) eraseBelow() {
	if w.cursor < len(w.rows) {
		w.rows = w.rows[:w.cursor]
	}
}

// eraseAll drops the whole pending window (CSI 2J / 3J).
func (w *rowWindow) eraseAll() {
	w.rows = w.rows[:0]
	w.cursor = 0
}

// flush writes every remaining pending row in order, e.g. once the session
// ends and no further redraw can occur.
func (w *rowWindow) flush() error {
	for _, row := range w.rows {
		if err := w.write(row); err != nil {
			return err
		}
	}
	w.rows = nil
	w.cursor = 0
	return nil
}

// write outputs one row (and its optional metadata line).
func (w *rowWindow) write(row pendingRow) error {
	if len(row.meta) > 0 {
		if _, err := w.out.Write(row.meta); err != nil {
			return err
		}
	}
	_, err := w.out.Write(append(row.text, '\n'))
	return err
}

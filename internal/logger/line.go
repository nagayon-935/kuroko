package logger

import (
	"bytes"

	"github.com/nagayon-935/kuroko/internal/textwidth"
)

// wideContinuation marks the second terminal column occupied by an East
// Asian Wide/Fullwidth rune (see internal/textwidth). It lets lineBuffer be
// indexed directly by column — matching how a real terminal's cell grid
// works — while renderLine skips these placeholder cells when reassembling
// the actual text.
const wideContinuation rune = 0

// gapCell marks a column that was skipped over by cursor movement (CSI C)
// and never written. It renders as a space, but unlike a written space it
// shows where separately drawn text begins — see lineBuffer.text.
const gapCell rune = -1

// lineBuffer models the terminal row currently being written: one cell per
// display column (wide runes occupy two; see wideContinuation) and the cursor
// column within it.
type lineBuffer struct {
	cells []rune
	col   int // cursor column (display columns, not bytes or runes)
	// steppedBack is set once the cursor has been moved left within the row
	// (\b or CSI D) since the last carriage return; see text.
	steppedBack bool
}

// text renders the row as it should be logged.
//
// After a carriage return the row is rendered up to the cursor: readline's
// accept-line redraw reprints a shorter line over the old one without
// erasing the rest, and the stale tail must not be logged.
//
// Once the cursor has stepped left inside the row, the text to its right is
// still on screen and belongs to the line — the user moved the cursor back
// into a command and pressed Enter there — so the row is rendered past the
// cursor, up to the first gap (columns skipped by CSI C and never written)
// at or after it. A right-aligned prompt (zsh RPROMPT) is drawn exactly that
// way: the shell jumps right, prints it, and jumps back, so the gap keeps it
// out of the command line. Trailing blanks right of the cursor are dropped
// too: they are what "\b \b" leaves behind when erasing the last character.
func (b *lineBuffer) text() []byte {
	end := min(b.col, len(b.cells))
	if b.steppedBack {
		for end < len(b.cells) && b.cells[end] != gapCell {
			end++
		}
		for end > b.col && b.cells[end-1] == ' ' {
			end--
		}
	}
	return renderLine(b.cells[:end])
}

// textToCursor renders the row up to the cursor, whatever lies right of it.
func (b *lineBuffer) textToCursor() []byte {
	return renderLine(b.cells[:min(b.col, len(b.cells))])
}

// reset empties the row and returns the cursor to column 0.
func (b *lineBuffer) reset() {
	b.cells = b.cells[:0]
	b.col = 0
	b.steppedBack = false
}

// home returns the cursor to column 0 without clearing the row (\r).
func (b *lineBuffer) home() {
	b.col = 0
	b.steppedBack = false
}

// backspace moves the cursor one column left (\b), stopping at column 0.
func (b *lineBuffer) backspace() {
	if b.col > 0 {
		b.col--
		b.steppedBack = true
	}
}

// forward moves the cursor n columns right (CSI C); later writes pad the
// gap with spaces.
func (b *lineBuffer) forward(n int) {
	if n > 0 {
		b.col = min(b.col+n, len(b.cells)+maxCSICount)
	}
}

// backward moves the cursor n columns left (CSI D), stopping at column 0.
func (b *lineBuffer) backward(n int) {
	if n > 0 && b.col > 0 {
		b.col = max(b.col-n, 0)
		b.steppedBack = true
	}
}

// eraseToEnd clears from the cursor to the end of the row (CSI K / 0K).
func (b *lineBuffer) eraseToEnd() {
	if b.col < len(b.cells) {
		if b.cells[b.col] == wideContinuation && b.col > 0 {
			b.cells[b.col-1] = ' '
		}
		b.cells = b.cells[:b.col]
	}
}

// deleteChars removes n cells at the cursor, shifting the rest left (CSI P).
func (b *lineBuffer) deleteChars(n int) {
	if n <= 0 || b.col >= len(b.cells) {
		return
	}
	if b.cells[b.col] == wideContinuation && b.col > 0 {
		b.cells[b.col-1] = ' '
	}
	if n < len(b.cells)-b.col {
		if b.cells[b.col+n] == wideContinuation {
			b.cells[b.col+n] = ' '
		}
		copy(b.cells[b.col:], b.cells[b.col+n:])
		b.cells = b.cells[:len(b.cells)-n]
	} else {
		b.cells = b.cells[:b.col]
	}
}

// put writes r at the cursor's current column — padding the row with
// spaces if the cursor was previously moved past its end, e.g. by CSI C —
// and advances the cursor by r's terminal display width. A wide rune
// occupies two consecutive columns: the rune itself in the first, and
// wideContinuation in the second, so that an edit later overwriting only
// the second column doesn't leave the buffer holding a rune through just
// one of its two cells.
func (b *lineBuffer) put(r rune) {
	w := textwidth.Rune(r)
	if w <= 0 {
		// Control/combining runes: occupy one column rather than zero, to
		// keep the column model simple. These are rare in the shell
		// prompts and NW device output kuroko targets.
		w = 1
	}
	b.setCell(b.col, r)
	for i := 1; i < w; i++ {
		b.setCell(b.col+i, wideContinuation)
	}
	b.col += w
}

// setCell writes r at column col, padding the row with spaces up to col if
// the cursor had previously moved past the end of the buffer. If overwriting
// col splits an existing wide rune, its remaining half is replaced with a
// space so column alignment is preserved.
func (b *lineBuffer) setCell(col int, r rune) {
	for len(b.cells) < col {
		b.cells = append(b.cells, gapCell)
	}
	if col < len(b.cells) {
		if b.cells[col] == wideContinuation && col > 0 {
			b.cells[col-1] = ' '
		}
		if col+1 < len(b.cells) && b.cells[col+1] == wideContinuation {
			b.cells[col+1] = ' '
		}
		b.cells[col] = r
	} else {
		b.cells = append(b.cells, r)
	}
}

// renderLine reassembles the text represented by a column-indexed cell
// slice, skipping wideContinuation placeholder cells and rendering gaps as
// spaces. It is the inverse of
// lineBuffer.put, which spreads one rune across 1 or 2 columns.
func renderLine(cells []rune) []byte {
	var buf bytes.Buffer
	buf.Grow(len(cells))
	for _, r := range cells {
		switch r {
		case wideContinuation:
			continue
		case gapCell:
			r = ' '
		}
		buf.WriteRune(r)
	}
	return buf.Bytes()
}

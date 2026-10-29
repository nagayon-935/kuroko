package logger

import (
	"bytes"

	"github.com/ryu/kuroko/internal/textwidth"
)

// wideContinuation marks the second terminal column occupied by an East
// Asian Wide/Fullwidth rune (see internal/textwidth). It lets lineBuffer be
// indexed directly by column — matching how a real terminal's cell grid
// works — while renderLine skips these placeholder cells when reassembling
// the actual text.
const wideContinuation rune = 0

// lineBuffer models the terminal row currently being written: one cell per
// display column (wide runes occupy two; see wideContinuation) and the cursor
// column within it. The row is rendered up to the cursor when committed,
// which is what makes readline's in-place edits come out right.
type lineBuffer struct {
	cells []rune
	col   int // cursor column (display columns, not bytes or runes)
}

// text renders the row up to the cursor.
func (b *lineBuffer) text() []byte {
	return renderLine(b.cells[:min(b.col, len(b.cells))])
}

// reset empties the row and returns the cursor to column 0.
func (b *lineBuffer) reset() {
	b.cells = b.cells[:0]
	b.col = 0
}

// home returns the cursor to column 0 without clearing the row (\r).
func (b *lineBuffer) home() { b.col = 0 }

// backspace moves the cursor one column left (\b), stopping at column 0.
func (b *lineBuffer) backspace() {
	if b.col > 0 {
		b.col--
	}
}

// forward moves the cursor n columns right (CSI C); later writes pad the
// gap with spaces.
func (b *lineBuffer) forward(n int) { b.col += n }

// backward moves the cursor n columns left (CSI D), stopping at column 0.
func (b *lineBuffer) backward(n int) { b.col = max(b.col-n, 0) }

// eraseToEnd clears from the cursor to the end of the row (CSI K / 0K).
func (b *lineBuffer) eraseToEnd() {
	if b.col < len(b.cells) {
		b.cells = b.cells[:b.col]
	}
}

// deleteChars removes n cells at the cursor, shifting the rest left (CSI P).
func (b *lineBuffer) deleteChars(n int) {
	if b.col >= len(b.cells) {
		return
	}
	if b.col+n < len(b.cells) {
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
// the cursor had previously moved past the end of the buffer.
func (b *lineBuffer) setCell(col int, r rune) {
	for len(b.cells) < col {
		b.cells = append(b.cells, ' ')
	}
	if col < len(b.cells) {
		b.cells[col] = r
	} else {
		b.cells = append(b.cells, r)
	}
}

// renderLine reassembles the text represented by a column-indexed cell
// slice, skipping wideContinuation placeholder cells. It is the inverse of
// lineBuffer.put, which spreads one rune across 1 or 2 columns.
func renderLine(cells []rune) []byte {
	var buf bytes.Buffer
	buf.Grow(len(cells))
	for _, r := range cells {
		if r == wideContinuation {
			continue
		}
		buf.WriteRune(r)
	}
	return buf.Bytes()
}

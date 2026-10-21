package tui

import "fmt"

const (
	// Home moves the cursor to the top-left corner without clearing, so
	// frames are redrawn in place without flicker.
	Home = "\x1b[H"
	// ClearEOL erases from the cursor to the end of the line.
	ClearEOL = "\x1b[K"

	reset = "\x1b[0m"
)

// Inverse renders s as black-on-white, the style used for header and
// footer bars and the selected row.
func Inverse(s string) string {
	return "\x1b[30;47m" + s + reset
}

// Yellow renders s in yellow.
func Yellow(s string) string {
	return "\x1b[33m" + s + reset
}

// MoveToRow moves the cursor to column 1 of the given 1-based row.
func MoveToRow(row int) string {
	return fmt.Sprintf("\x1b[%d;1H", row)
}

// FollowSelection returns the scroll offset that keeps selected within the
// visible window [scroll, scroll+bodyHeight), moving it the minimum amount
// necessary, so lists auto-scroll to the current selection every frame.
func FollowSelection(selected, scroll, bodyHeight int) int {
	if bodyHeight <= 0 {
		return scroll
	}
	if selected < scroll {
		return selected
	}
	if selected >= scroll+bodyHeight {
		return selected - bodyHeight + 1
	}
	return scroll
}

// ClickRowToIndex converts a mouse click's on-screen row r (already
// adjusted for the header) into an index into the current, possibly
// scrolled, list. Returns -1 if the click landed outside the populated
// rows.
func ClickRowToIndex(r, scroll, count int) int {
	if r < 0 {
		return -1
	}
	idx := r + scroll
	if idx < 0 || idx >= count {
		return -1
	}
	return idx
}

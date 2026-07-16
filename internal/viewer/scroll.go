package viewer

// followSelection returns the scroll offset that keeps selected within the
// visible window [scroll, scroll+bodyHeight), moving it the minimum amount
// necessary. It is shared by Viewer's timeline pane and LogSelector's log
// list so both auto-scroll to the current selection every frame instead of
// only ever rendering the first bodyHeight rows.
func followSelection(selected, scroll, bodyHeight int) int {
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

// clickRowToIndex converts a mouse click's on-screen row r (already
// adjusted for the header) into an index into the current, possibly
// scrolled, list. Returns -1 if the click landed outside the populated
// rows.
func clickRowToIndex(r, scroll, count int) int {
	if r < 0 {
		return -1
	}
	idx := r + scroll
	if idx < 0 || idx >= count {
		return -1
	}
	return idx
}

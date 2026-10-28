package tui

import "testing"

func TestFollowSelection(t *testing.T) {
	tests := []struct {
		name       string
		selected   int
		scroll     int
		bodyHeight int
		want       int
	}{
		{"selection already visible: no change", 5, 0, 10, 0},
		{"selection above viewport: scrolls up to it", 2, 5, 10, 2},
		{"selection below viewport: scrolls down to reveal it", 15, 0, 10, 6},
		{"selection at exact bottom edge: scrolls by one", 10, 0, 10, 1},
		{"zero body height: scroll unchanged", 5, 3, 0, 3},
		{"negative body height: scroll unchanged", 5, 3, -1, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FollowSelection(tt.selected, tt.scroll, tt.bodyHeight)
			if got != tt.want {
				t.Errorf("FollowSelection(%d, %d, %d) = %d; want %d", tt.selected, tt.scroll, tt.bodyHeight, got, tt.want)
			}
		})
	}
}

func TestClickRowToIndex(t *testing.T) {
	tests := []struct {
		name   string
		r      int
		scroll int
		count  int
		want   int
	}{
		{"negative row: out of range", -1, 0, 10, -1},
		{"row within count, no scroll", 3, 0, 10, 3},
		{"row plus scroll within count", 3, 5, 10, 8},
		{"row plus scroll beyond count", 3, 8, 10, -1},
		{"row beyond count with zero scroll", 20, 0, 10, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClickRowToIndex(tt.r, tt.scroll, tt.count)
			if got != tt.want {
				t.Errorf("ClickRowToIndex(%d, %d, %d) = %d; want %d", tt.r, tt.scroll, tt.count, got, tt.want)
			}
		})
	}
}

func TestPlainNeutralizesControlAndFormatRunes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"printable ascii and cjk unchanged", "host-01 接続先", "host-01 接続先"},
		{"ansi escape and tabs replaced", "a\x1b[2Jb\tc\r\nd\x7fe", "a [2Jb c  d e"},
		{"c1 control and bidi override replaced", "admin\u0085\u202ehost", "admin  host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Plain(tt.input); got != tt.want {
				t.Errorf("Plain(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRenderHelpers(t *testing.T) {
	if got, want := Inverse("bar"), "\x1b[30;47mbar\x1b[0m"; got != want {
		t.Errorf("Inverse = %q; want %q", got, want)
	}
	if got, want := Yellow("|"), "\x1b[33m|\x1b[0m"; got != want {
		t.Errorf("Yellow = %q; want %q", got, want)
	}
	if got, want := MoveToRow(24), "\x1b[24;1H"; got != want {
		t.Errorf("MoveToRow = %q; want %q", got, want)
	}
}

package textwidth

import (
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"shorter than width is unchanged", "abc", 10, "abc"},
		{"exact width is unchanged", "abcde", 5, "abcde"},
		{"ascii truncates with ellipsis", "abcdefghij", 5, "ab..."},
		{
			"multi-byte input truncates on rune boundaries, not bytes",
			// Each 接/続/先 is a 3-byte, display-width-2 UTF-8 rune; only
			// one fits within the 2-column budget left after reserving 3
			// columns for "...".
			"接続先router1",
			5,
			"接...",
		},
		{"multi-byte input shorter than width is unchanged", "接続先", 10, "接続先"},
		{"width of exactly 3 truncates with no room for ellipsis", "abcdefgh", 3, "abc"},
		{"width less than 3 truncates with no ellipsis", "abcdefgh", 2, "ab"},
		{"width zero returns empty string", "abcdefgh", 0, ""},
		{"negative width returns empty string", "abcdefgh", -1, ""},
		{"empty input returns empty string", "", 5, ""},
		{
			// C4: wide characters occupy 2 terminal columns, so truncation
			// must be based on display width, not rune count, or the right
			// border of the pane drifts out of alignment.
			"wide characters truncate by display width, not rune count",
			"漢字テスト",
			4,
			"...",
		},
		{"wide characters exactly filling width are unchanged", "漢字", 4, "漢字"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q; want %q", tt.input, tt.width, got, tt.want)
			}
			// Truncate must never split a multi-byte rune: the
			// result must always be valid UTF-8.
			if !utf8.ValidString(got) {
				t.Errorf("Truncate(%q, %d) = %q; not valid UTF-8", tt.input, tt.width, got)
			}
			if tt.width > 0 {
				if n := String(got); n > tt.width {
					t.Errorf("Truncate(%q, %d) = %q; display width %d exceeds requested width %d", tt.input, tt.width, got, n, tt.width)
				}
			}
		})
	}
}

func TestFit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"pads short ascii to width", "ab", 5, "ab   "},
		{"exact width is unchanged", "abcde", 5, "abcde"},
		{"truncates long input with ellipsis", "abcdefghij", 5, "ab..."},
		{"pads by display width for wide characters", "漢字", 6, "漢字  "},
		{"zero width returns empty string", "abc", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Fit(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("Fit(%q, %d) = %q; want %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

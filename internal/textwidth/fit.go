package textwidth

import "strings"

// Truncate truncates s to at most width terminal columns, appending "..."
// when truncated. Width is measured with String rather than rune count,
// since East Asian Wide/Fullwidth characters (e.g. Japanese hostnames or
// device output) occupy two columns each; truncating by rune count alone
// drifts a pane's right border out of alignment. Truncation always stops on
// a rune boundary, so the result is valid UTF-8.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if String(s) <= width {
		return s
	}
	budget := width
	suffix := ""
	if width > 3 {
		budget = width - 3
		suffix = "..."
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		w := Rune(r)
		if col+w > budget {
			break
		}
		b.WriteRune(r)
		col += w
	}
	return b.String() + suffix
}

// Fit truncates or right-pads s with spaces so its terminal display width
// matches width columns.
func Fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if n := String(s); n > width {
		return Truncate(s, width)
	} else {
		return s + strings.Repeat(" ", width-n)
	}
}

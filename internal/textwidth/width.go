// Package textwidth computes terminal display width for runes and strings,
// distinguishing East Asian Wide/Fullwidth characters (2 columns) from
// narrow characters (1 column) and zero-width marks/control codes (0
// columns). This mirrors the layout kuroko's TUI (internal/viewer) needs
// when rendering Japanese network-device output, without pulling in an
// external dependency.
package textwidth

import "unicode"

// wideRanges lists the Unicode code point ranges whose East Asian Width
// property is Wide (W) or Fullwidth (F), which occupy two terminal columns.
// Ranges cover the scripts kuroko is expected to render: Hiragana, Katakana,
// CJK Unified Ideographs (common + extension A), Hangul syllables, CJK/Ideographic
// punctuation, fullwidth forms, and wide emoji/symbol blocks.
var wideRanges = []struct{ lo, hi rune }{
	{0x1100, 0x115F},   // Hangul Jamo
	{0x2E80, 0x303E},   // CJK Radicals, Kangxi Radicals, CJK Symbols and Punctuation
	{0x3041, 0x33FF},   // Hiragana, Katakana, Bopomofo, Hangul Compat Jamo, CJK misc
	{0x3400, 0x4DBF},   // CJK Unified Ideographs Extension A
	{0x4E00, 0x9FFF},   // CJK Unified Ideographs
	{0xA000, 0xA4CF},   // Yi Syllables/Radicals
	{0xAC00, 0xD7A3},   // Hangul Syllables
	{0xF900, 0xFAFF},   // CJK Compatibility Ideographs
	{0xFE30, 0xFE4F},   // CJK Compatibility Forms
	{0xFF01, 0xFF60},   // Fullwidth Forms (!-～ and punctuation)
	{0xFFE0, 0xFFE6},   // Fullwidth Signs
	{0x1F300, 0x1F64F}, // Misc Symbols and Pictographs, Emoticons
	{0x1F900, 0x1F9FF}, // Supplemental Symbols and Pictographs
	{0x20000, 0x3FFFD}, // CJK Unified Ideographs Extension B and beyond
}

// Rune returns the terminal display width of r: 0 for control characters and
// zero-width combining marks, 2 for East Asian Wide/Fullwidth characters,
// and 1 for everything else (including East Asian "ambiguous width"
// characters, which are rendered narrow by convention).
func Rune(r rune) int {
	if r == 0 || unicode.IsControl(r) {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	for _, wr := range wideRanges {
		if r >= wr.lo && r <= wr.hi {
			return 2
		}
	}
	return 1
}

// String returns the total terminal display width of s, summing Rune(r)
// over each rune.
func String(s string) int {
	width := 0
	for _, r := range s {
		width += Rune(r)
	}
	return width
}

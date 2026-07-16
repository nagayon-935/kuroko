package textwidth

import "testing"

func TestRune(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		want int
	}{
		{"ascii letter", 'A', 1},
		{"ascii digit", '0', 1},
		{"ascii space", ' ', 1},
		{"hiragana", 'あ', 2},
		{"katakana", 'ト', 2},
		{"kanji", '漢', 2},
		{"fullwidth latin A", 'Ａ', 2},
		{"fullwidth punctuation", '、', 2},
		{"halfwidth katakana", 'ｱ', 1},
		{"hangul syllable", '한', 2},
		{"combining acute accent", '́', 0},
		{"control char NUL", '\x00', 0},
		{"control char BEL", '\a', 0},
		{"emoji (wide)", '🎉', 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Rune(tt.r); got != tt.want {
				t.Errorf("Rune(%q) = %d, want %d", tt.r, got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want int
	}{
		{"empty", "", 0},
		{"ascii only", "hello", 5},
		{"japanese only", "日本語", 6},
		{"mixed ascii and japanese", "host01-スイッチ", 15},
		{"plain ascii word", "Kelvin", 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := String(tt.s); got != tt.want {
				t.Errorf("String(%q) = %d, want %d", tt.s, got, tt.want)
			}
		})
	}
}

package tui

import "testing"

func TestParseMouse(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		want      Mouse
		wantMouse bool
	}{
		{"left click press", "\x1b[<0;12;5M", Mouse{Button: MouseLeft, X: 12, Y: 5}, true},
		{"wheel up", "\x1b[<64;1;1M", Mouse{Button: MouseWheelUp, X: 1, Y: 1}, true},
		{"wheel down release form", "\x1b[<65;40;20m", Mouse{Button: MouseWheelDown, X: 40, Y: 20}, true},
		{"malformed report is swallowed as mouse input", "\x1b[<garbage", Mouse{Button: -1}, true},
		{"arrow key is not mouse input", "\x1b[A", Mouse{}, false},
		{"plain byte is not mouse input", "j", Mouse{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseMouse([]byte(tt.key))
			if ok != tt.wantMouse || got != tt.want {
				t.Errorf("ParseMouse(%q) = %+v, %v; want %+v, %v", tt.key, got, ok, tt.want, tt.wantMouse)
			}
		})
	}
}

func TestCSIKey(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		want   byte
		wantOK bool
	}{
		{"up arrow", "\x1b[A", 'A', true},
		{"left arrow", "\x1b[D", 'D', true},
		{"page up is not a three-byte CSI key", "\x1b[5~", 0, false},
		{"plain byte", "k", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CSIKey([]byte(tt.key))
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("CSIKey(%q) = %q, %v; want %q, %v", tt.key, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestTildeKey(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		want   byte
		wantOK bool
	}{
		{"page up", "\x1b[5~", '5', true},
		{"page down", "\x1b[6~", '6', true},
		{"arrow key", "\x1b[B", 0, false},
		{"wrong terminator", "\x1b[5A", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := TildeKey([]byte(tt.key))
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("TildeKey(%q) = %q, %v; want %q, %v", tt.key, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestEditLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		key      []byte
		wantLine string
		want     EditResult
	}{
		{"printable byte appends", "ab", []byte("c"), "abc", EditChanged},
		{"delete removes last byte", "abc", []byte{KeyDelete}, "ab", EditChanged},
		{"backspace removes last byte", "abc", []byte{KeyBackspace}, "ab", EditChanged},
		{"backspace on empty line is ignored", "", []byte{KeyDelete}, "", EditIgnored},
		{"carriage return submits", "abc", []byte{KeyCR}, "abc", EditSubmit},
		{"line feed submits", "abc", []byte{KeyLF}, "abc", EditSubmit},
		{"escape cancels", "abc", []byte{KeyEsc}, "abc", EditCancel},
		{"ctrl+c interrupts", "abc", []byte{KeyCtrlC}, "abc", EditInterrupt},
		{"other control byte is ignored", "abc", []byte{KeyTab}, "abc", EditIgnored},
		{"multi-byte sequence is ignored", "abc", []byte("\x1b[A"), "abc", EditIgnored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLine, got := EditLine(tt.line, tt.key)
			if gotLine != tt.wantLine || got != tt.want {
				t.Errorf("EditLine(%q, %q) = %q, %v; want %q, %v", tt.line, tt.key, gotLine, got, tt.wantLine, tt.want)
			}
		})
	}
}

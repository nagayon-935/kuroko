package tui

import (
	"bytes"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Control bytes delivered by a terminal in raw mode.
const (
	KeyCtrlB     = 2
	KeyCtrlC     = 3
	KeyCtrlD     = 4
	KeyBackspace = 8
	KeyTab       = 9
	KeyLF        = 10
	KeyCR        = 13
	KeyCtrlN     = 14
	KeyCtrlU     = 21
	KeyEsc       = 27
	KeyDelete    = 127
)

// SGR mouse button codes.
const (
	MouseLeft      = 0
	MouseWheelUp   = 64
	MouseWheelDown = 65
)

// readBufSize bounds a single key read; it comfortably fits SGR mouse
// reports, CSI sequences, multi-byte UTF-8 runes, and short pastes.
const readBufSize = 64

var mousePrefix = []byte("\x1b[<")

// Mouse is a decoded SGR (1006) mouse report. X and Y are 1-based terminal
// columns and rows. Release is true for a button-release ('m') report.
// Button is -1 for a malformed report.
type Mouse struct {
	Button, X, Y int
	Release      bool
}

// ReadKey blocks until stdin delivers input and returns the raw bytes of
// one read. It returns an empty slice when the read yields no bytes.
func ReadKey() ([]byte, error) {
	buf := make([]byte, readBufSize)
	n, err := os.Stdin.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// ParseMouse reports whether key is an SGR mouse report and decodes it.
// A report that starts like a mouse sequence but cannot be decoded is
// still reported as mouse input (so callers swallow it) with Button -1.
func ParseMouse(key []byte) (Mouse, bool) {
	if !bytes.HasPrefix(key, mousePrefix) {
		return Mouse{}, false
	}
	rest := key[len(mousePrefix):]
	btn, rest, ok := parseUintField(rest, ';')
	if !ok {
		return Mouse{Button: -1}, true
	}
	x, rest, ok := parseUintField(rest, ';')
	if !ok {
		return Mouse{Button: -1}, true
	}
	y, rest, ok := parseMouseTail(rest)
	if !ok {
		return Mouse{Button: -1}, true
	}
	return Mouse{Button: btn, X: x, Y: y, Release: rest[0] == 'm'}, true
}

func parseUintField(b []byte, sep byte) (val int, rest []byte, ok bool) {
	i := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		val = val*10 + int(b[i]-'0')
		i++
	}
	if i == 0 || i >= len(b) || b[i] != sep {
		return 0, nil, false
	}
	return val, b[i+1:], true
}

func parseMouseTail(b []byte) (val int, rest []byte, ok bool) {
	i := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		val = val*10 + int(b[i]-'0')
		i++
	}
	if i == 0 || i >= len(b) || (b[i] != 'M' && b[i] != 'm') {
		return 0, nil, false
	}
	return val, b[i:], true
}

// CSIKey returns the final byte of a three-byte CSI sequence such as an
// arrow key (ESC [ A..D) or Home/End (ESC [ H / ESC [ F).
func CSIKey(key []byte) (byte, bool) {
	if len(key) == 3 && key[0] == KeyEsc && key[1] == '[' {
		return key[2], true
	}
	return 0, false
}

// SS3Key returns the final byte of a three-byte SS3 sequence (ESC O H / F),
// which application-cursor terminals emit for Home and End.
func SS3Key(key []byte) (byte, bool) {
	if len(key) == 3 && key[0] == KeyEsc && key[1] == 'O' {
		return key[2], true
	}
	return 0, false
}

// TildeKey returns the parameter byte of a four-byte "ESC [ n ~" sequence,
// e.g. '5' for PageUp and '6' for PageDown.
func TildeKey(key []byte) (byte, bool) {
	if len(key) == 4 && key[0] == KeyEsc && key[1] == '[' && key[3] == '~' {
		return key[2], true
	}
	return 0, false
}

// IsHomeKey reports whether key is a terminal Home key sequence.
func IsHomeKey(key []byte) bool {
	if c, ok := CSIKey(key); ok && c == 'H' {
		return true
	}
	if c, ok := SS3Key(key); ok && c == 'H' {
		return true
	}
	if c, ok := TildeKey(key); ok && (c == '1' || c == '7') {
		return true
	}
	return false
}

// IsEndKey reports whether key is a terminal End key sequence.
func IsEndKey(key []byte) bool {
	if c, ok := CSIKey(key); ok && c == 'F' {
		return true
	}
	if c, ok := SS3Key(key); ok && c == 'F' {
		return true
	}
	if c, ok := TildeKey(key); ok && (c == '4' || c == '8') {
		return true
	}
	return false
}

// PageDir returns -1 for PageUp, +1 for PageDown, or 0 otherwise.
func PageDir(key []byte) int {
	if c, ok := TildeKey(key); ok {
		switch c {
		case '5':
			return -1
		case '6':
			return 1
		}
	}
	return 0
}

// EditResult describes what EditLine did with a key.
type EditResult int

const (
	// EditIgnored means the key did not affect the line.
	EditIgnored EditResult = iota
	// EditChanged means the returned line differs from the input.
	EditChanged
	// EditSubmit means Enter was pressed.
	EditSubmit
	// EditCancel means Esc was pressed.
	EditCancel
	// EditInterrupt means Ctrl+C was pressed.
	EditInterrupt
)

// EditLine applies a key read to a search prompt: control keys submit, cancel,
// clear (Ctrl+U), or erase a UTF-8 rune, escape sequences are ignored, and
// printable UTF-8 text (including Japanese characters and pasted text) is
// appended.
func EditLine(line string, key []byte) (string, EditResult) {
	if len(key) == 0 {
		return line, EditIgnored
	}
	if len(key) == 1 {
		switch b := key[0]; b {
		case KeyCR, KeyLF:
			return line, EditSubmit
		case KeyEsc:
			return line, EditCancel
		case KeyCtrlC:
			return line, EditInterrupt
		case KeyCtrlU:
			if line == "" {
				return line, EditIgnored
			}
			return "", EditChanged
		case KeyDelete, KeyBackspace:
			if line == "" {
				return line, EditIgnored
			}
			_, size := utf8.DecodeLastRuneInString(line)
			return line[:len(line)-size], EditChanged
		default:
			if b >= 32 && b < 127 {
				return line + string(b), EditChanged
			}
			return line, EditIgnored
		}
	}
	if key[0] == KeyEsc || !utf8.Valid(key) {
		return line, EditIgnored
	}
	var b strings.Builder
	b.WriteString(line)
	added := false
	for len(key) > 0 {
		r, size := utf8.DecodeRune(key)
		key = key[size:]
		if unicode.IsPrint(r) && !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
			b.WriteRune(r)
			added = true
		}
	}
	if !added {
		return line, EditIgnored
	}
	return b.String(), EditChanged
}

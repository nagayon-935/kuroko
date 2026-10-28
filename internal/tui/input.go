package tui

import (
	"bytes"
	"os"
	"regexp"
	"strconv"
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
// reports and CSI sequences.
const readBufSize = 16

var (
	mousePrefix = []byte("\x1b[<")
	mouseRegexp = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)([Mm])`)
)

// Mouse is a decoded SGR (1006) mouse report. X and Y are 1-based terminal
// columns and rows. Button is -1 for a malformed report.
type Mouse struct {
	Button, X, Y int
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
	m := mouseRegexp.FindSubmatch(key)
	if len(m) != 5 {
		return Mouse{Button: -1}, true
	}
	btn, _ := strconv.Atoi(string(m[1]))
	x, _ := strconv.Atoi(string(m[2]))
	y, _ := strconv.Atoi(string(m[3]))
	return Mouse{Button: btn, X: x, Y: y}, true
}

// CSIKey returns the final byte of a three-byte CSI sequence such as an
// arrow key (ESC [ A..D).
func CSIKey(key []byte) (byte, bool) {
	if len(key) == 3 && key[0] == KeyEsc && key[1] == '[' {
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

// EditLine applies a single-byte key to a search prompt that accepts
// printable ASCII. Multi-byte keys are ignored.
func EditLine(line string, key []byte) (string, EditResult) {
	if len(key) != 1 {
		return line, EditIgnored
	}
	switch b := key[0]; b {
	case KeyCR, KeyLF:
		return line, EditSubmit
	case KeyEsc:
		return line, EditCancel
	case KeyCtrlC:
		return line, EditInterrupt
	case KeyDelete, KeyBackspace:
		if line == "" {
			return line, EditIgnored
		}
		return line[:len(line)-1], EditChanged
	default:
		if b >= 32 && b < 127 {
			return line + string(b), EditChanged
		}
		return line, EditIgnored
	}
}

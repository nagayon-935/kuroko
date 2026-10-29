package logger

var (
	enterAltScreen = []byte("\x1b[?1049h") // vim / less / htop enter
	exitAltScreen  = []byte("\x1b[?1049l") // alternate screen exit
)

// normalizeLineEndings collapses any run of \r immediately followed by \n
// into a single \n, and leaves bare \r (not followed by \n) untouched.
//
// The local PTY driver (ONLCR) prepends \r before every \n it outputs, so
// the remote's \r\n becomes \r\r\n.  Any number of consecutive \r before \n
// is therefore an artifact and should be reduced to just the \n so that
// the terminal sees a plain newline instead of a cursor-return + newline.
// Bare \r (overwrite/carriage-return without newline) is preserved so that
// the terminal can simulate readline's in-place line editing.
func normalizeLineEndings(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] != '\r' {
			out = append(out, p[i])
			continue
		}
		// Scan past consecutive \r characters.
		j := i + 1
		for j < len(p) && p[j] == '\r' {
			j++
		}
		if j < len(p) && p[j] == '\n' {
			// \r+\n → skip the \r run; the loop will emit the \n next.
			i = j - 1
			continue
		}
		// Bare \r — keep it for the terminal.
		out = append(out, '\r')
	}
	return out
}

// incompleteEscapePrefix returns the number of trailing bytes in data that
// look like the start of an incomplete ANSI escape sequence.  Those bytes
// should be buffered and prepended to the next Write call.
func incompleteEscapePrefix(data []byte) int {
	const maxScan = 32
	start := len(data) - maxScan
	if start < 0 {
		start = 0
	}
	for i := len(data) - 1; i >= start; i-- {
		if data[i] != 0x1b {
			continue
		}
		tail := data[i:]
		if isCompleteEscape(tail) {
			break // complete sequence at the end — nothing to buffer
		}
		return len(tail)
	}
	return 0
}

// isCompleteEscape reports whether seq (beginning with ESC) is a fully
// terminated escape sequence.
func isCompleteEscape(seq []byte) bool {
	if len(seq) < 2 || seq[0] != 0x1b {
		return false
	}
	switch seq[1] {
	case '[': // CSI — terminated by byte in [@-~]
		for _, c := range seq[2:] {
			if c >= '@' && c <= '~' {
				return true
			}
		}
		return false
	case ']': // OSC — terminated by BEL or ESC
		for _, c := range seq[2:] {
			if c == 0x07 || c == 0x1b {
				return true
			}
		}
		return false
	default: // two-char sequence
		return len(seq) >= 2
	}
}

// csiCount returns the repeat count N of a CSI sequence from its parameter
// string: the first run of digits, or 1 when there is none (or it is 0).
func csiCount(params string) int {
	var val int
	var found bool
	for i := 0; i < len(params); i++ {
		c := params[i]
		if c >= '0' && c <= '9' {
			val = val*10 + int(c-'0')
			found = true
		} else if found {
			break
		}
	}
	if found && val > 0 {
		return val
	}
	return 1
}

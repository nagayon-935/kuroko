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
// look like the start of an incomplete CSI sequence.  Those bytes should be
// buffered and prepended to the next Write call, so that the alternate-screen
// switches (which are CSI sequences) are found even when a write splits them.
// Other sequences need no buffering: the terminal's escapeParser carries any
// sequence across writes by itself, and holding back an OSC tail here would
// only delay the text after it.
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
		if len(tail) == 1 || (tail[1] == '[' && !isCompleteEscape(tail)) {
			return len(tail)
		}
		return 0
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
	case ']': // OSC — terminated by BEL or ST (ESC \)
		for i, c := range seq[2:] {
			if c == 0x07 || (c == 0x1b && i+3 < len(seq) && seq[i+3] == '\\') {
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

type escState uint8

const (
	escGround       escState = iota // not inside an escape sequence
	escStart                        // saw ESC
	escIntermediate                 // ESC followed by intermediate bytes, e.g. "ESC (" of ESC ( B
	escCSI                          // ESC [ … up to a final byte
	escString                       // OSC/DCS/SOS/PM/APC payload, up to BEL or ST
	escStringEsc                    // saw ESC inside a string: ST if '\' follows
)

const (
	maxCSILen    = 64  // longer CSI sequences are consumed but not applied
	maxStringLen = 256 // longer OSC/DCS payloads are consumed; only this prefix is kept
)

// escapeSeq is one complete escape sequence that may affect the log.
type escapeSeq struct {
	kind byte   // '[' for CSI, ']' for OSC, 'P'/'X'/'^'/'_' for DCS/SOS/PM/APC; 0 otherwise
	data []byte // CSI: the whole sequence from ESC; strings: the payload
}

// escapeParser consumes escape sequences from the PTY stream the way xterm
// does, so none of their bytes leak into the log as text:
//   - CSI: ESC [ parameters final, the final byte in 0x40–0x7E
//   - OSC, DCS, SOS, PM, APC: ESC ] / P / X / ^ / _ payload, ended by BEL or
//     ST (ESC \); an ESC followed by anything else aborts the string and
//     starts a new sequence, and CAN/SUB cancel it
//   - nF: ESC, intermediates (0x20–0x2F), final, e.g. ESC ( B from tput sgr0
//   - two-byte: ESC final, e.g. ESC M or ESC =
//
// A sequence may be split across any number of writes.
type escapeParser struct {
	state escState
	kind  byte
	buf   []byte
}

func (p *escapeParser) active() bool { return p.state != escGround }

// start begins a sequence; the caller has just seen ESC.
func (p *escapeParser) start() {
	p.state = escStart
	p.buf = p.buf[:0]
}

// feed consumes the next byte of the active sequence and reports whether
// the sequence ended with it. The returned data is only valid until the
// next call.
func (p *escapeParser) feed(b byte) (escapeSeq, bool) {
	switch p.state {
	case escStart:
		switch {
		case b == 0x1b:
			p.start()
		case b == 0x18 || b == 0x1a:
			p.state = escGround
			return escapeSeq{}, true
		case b == '[':
			p.state, p.kind = escCSI, b
			p.buf = append(p.buf[:0], 0x1b, b)
		case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
			p.state, p.kind = escString, b
			p.buf = p.buf[:0]
		case b >= 0x20 && b <= 0x2f:
			p.state = escIntermediate
		default:
			p.state = escGround
			return escapeSeq{}, true
		}
	case escIntermediate:
		if b < 0x20 || b > 0x2f {
			p.state = escGround
			return escapeSeq{}, true
		}
	case escCSI:
		switch {
		case b == 0x1b:
			p.start()
		case b == 0x18 || b == 0x1a:
			p.state = escGround
			return escapeSeq{}, true
		case b >= 0x40 && b <= 0x7e:
			p.state = escGround
			if len(p.buf) >= maxCSILen {
				return escapeSeq{}, true
			}
			p.buf = append(p.buf, b)
			return escapeSeq{kind: '[', data: p.buf}, true
		case b >= 0x20 && len(p.buf) < maxCSILen:
			p.buf = append(p.buf, b)
		}
	case escString:
		switch b {
		case 0x07:
			p.state = escGround
			return escapeSeq{kind: p.kind, data: p.buf}, true
		case 0x1b:
			p.state = escStringEsc
		case 0x18, 0x1a:
			p.state = escGround
			return escapeSeq{}, true
		default:
			if len(p.buf) < maxStringLen {
				p.buf = append(p.buf, b)
			}
		}
	case escStringEsc:
		if b == '\\' {
			p.state = escGround
			return escapeSeq{kind: p.kind, data: p.buf}, true
		}
		p.start()
		return p.feed(b)
	}
	return escapeSeq{}, false
}

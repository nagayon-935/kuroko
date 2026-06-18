package logger

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ansiEscape matches ANSI/VT100 terminal control sequences:
//   - CSI:  ESC [ <params> <final>
//   - OSC:  ESC ] <text> BEL-or-ST
//   - two-char: ESC <byte>
var ansiEscape = regexp.MustCompile(
	`\x1b(?:` +
		`\[[0-9;?<=>!#%&'()*+,\-./]*[@-~]` + // CSI sequences
		`|\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC sequences
		`|[@-Z\\-_]` + // two-character sequences
		`)`)

// stripANSI removes ANSI/VT100 escape sequences, then normalises line endings.
func stripANSI(p []byte) []byte {
	return normalizeLineEndings(ansiEscape.ReplaceAll(p, nil))
}

// normalizeLineEndings collapses any run of \r immediately followed by \n
// into a single \n, and leaves bare \r (not followed by \n) untouched.
//
// The local PTY driver (ONLCR) prepends \r before every \n it outputs, so
// the remote's \r\n becomes \r\r\n.  Any number of consecutive \r before \n
// is therefore an artifact and should be reduced to just the \n so that
// processLine sees a plain newline instead of a cursor-return + newline.
// Bare \r (overwrite/carriage-return without newline) is preserved so that
// processLine can simulate readline's in-place line editing.
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
		// Bare \r — keep it for processLine.
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

var (
	enterAltScreen = []byte("\x1b[?1049h") // vim / less / htop enter
	exitAltScreen  = []byte("\x1b[?1049l") // alternate screen exit
)

// Logger writes session output to a file, applying ANSI stripping and a
// terminal line-buffer simulation so the log is human-readable plain text.
type Logger struct {
	file      *os.File
	rawFile   *os.File // non-nil when KUROKO_RAW_DEBUG=1; receives raw PTY bytes
	Path      string
	altScreen bool   // true while a full-screen app (vim/less) owns the terminal
	seqBuf    []byte // incomplete escape sequence carried across Write calls
	lineBuf   []byte // current output line being assembled
	lineCol   int    // cursor column within lineBuf
	pendingCR bool   // \r was the last control char seen; defer reset until next char
	writeSeq  uint64 // monotonic counter for raw debug chunks
}

func New(logDir string, args []string) (*Logger, error) {
	filename := generateFilename(args)
	path := uniquePath(logDir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}

	header := fmt.Sprintf(
		"# kuroko session log\n# Started : %s\n# Command : %s\n# %s\n\n",
		time.Now().Format(time.RFC3339),
		strings.Join(args, " "),
		strings.Repeat("-", 68),
	)
	if _, err := f.WriteString(header); err != nil {
		f.Close()
		return nil, err
	}

	l := &Logger{file: f, Path: path}

	if os.Getenv("KUROKO_RAW_DEBUG") == "1" {
		rawPath := path + ".raw"
		rf, err := os.OpenFile(rawPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err == nil {
			l.rawFile = rf
		}
	}

	return l, nil
}

func (l *Logger) Write(p []byte) (int, error) {
	if l.rawFile != nil {
		l.writeSeq++
		fmt.Fprintf(l.rawFile, "=== write %d (%d bytes) ===\n%q\n", l.writeSeq, len(p), p)
	}

	// Merge buffered partial escape sequence from the previous write so we
	// never split an escape sequence across buffer boundaries.
	data := append(l.seqBuf, p...)
	l.seqBuf = nil

	// Save any trailing incomplete ANSI escape sequence for the next write.
	if n := incompleteEscapePrefix(data); n > 0 {
		tail := data[len(data)-n:]
		l.seqBuf = make([]byte, n)
		copy(l.seqBuf, tail)
		data = data[:len(data)-n]
	}

	return len(p), l.writeFiltered(data)
}

// writeFiltered handles alternate-screen suppression.
// Full-screen apps (vim, less, htop) bracket their output with \x1b[?1049h
// (enter) and \x1b[?1049l (exit).  While in alternate-screen mode the raw
// PTY stream produces garbled content; we suppress it and log a marker.
func (l *Logger) writeFiltered(data []byte) error {
	for len(data) > 0 {
		if !l.altScreen {
			idx := bytes.Index(data, enterAltScreen)
			if idx < 0 {
				return l.processLine(stripANSI(data))
			}
			if err := l.processLine(stripANSI(data[:idx])); err != nil {
				return err
			}
			if _, err := l.file.WriteString("[full-screen app active — output suppressed]\n"); err != nil {
				return err
			}
			l.altScreen = true
			data = data[idx+len(enterAltScreen):]
		} else {
			idx := bytes.Index(data, exitAltScreen)
			if idx < 0 {
				return nil // still inside alternate screen; discard
			}
			l.altScreen = false
			data = data[idx+len(exitAltScreen):]
		}
	}
	return nil
}

// processLine feeds ANSI-stripped bytes through a terminal line-buffer
// simulation.  This handles the control characters that readline uses for
// in-place line editing:
//   - \r  (0x0D) carriage return — set pendingCR; the reset to column 0 is
//                deferred until the next printable character arrives.
//   - \b  (0x08) backspace — move cursor one column left
//   - \n  (0x0A) newline    — commit the current line to the file
//
// pendingCR is the key to handling the PTY driver's ONLCR behaviour: the
// driver converts \r\n → \r\r\n, and that sequence is often split across
// two successive write calls (\r at the end of one, \n at the start of the
// next).  By NOT resetting lineCol immediately on \r, the content built up
// in lineBuf survives until the \n commits it.  If a printable char follows
// \r instead (readline overwrite), the deferred reset fires then.
func (l *Logger) processLine(data []byte) error {
	for _, b := range data {
		switch b {
		case '\n':
			// If pendingCR is set the \r was the CR half of a CRLF pair —
			// do not reset lineCol; commit the content that is already there.
			l.pendingCR = false
			end := l.lineCol
			if end > len(l.lineBuf) {
				end = len(l.lineBuf)
			}
			if _, err := l.file.Write(append(l.lineBuf[:end:end], '\n')); err != nil {
				return err
			}
			l.lineBuf = l.lineBuf[:0]
			l.lineCol = 0
		case '\r':
			// Defer the column reset; resolve it when the next char arrives.
			l.pendingCR = true
		case '\b':
			if l.pendingCR {
				l.lineCol = 0
				l.pendingCR = false
			}
			if l.lineCol > 0 {
				l.lineCol--
			}
		default:
			if b < 0x20 {
				continue // discard other non-printable control bytes
			}
			if l.pendingCR {
				l.lineCol = 0
				l.pendingCR = false
			}
			if l.lineCol < len(l.lineBuf) {
				l.lineBuf[l.lineCol] = b
			} else {
				l.lineBuf = append(l.lineBuf, b)
			}
			l.lineCol++
		}
	}
	return nil
}

func (l *Logger) Close(exitCode int) error {
	// Flush any line that was not terminated with \n.
	if l.lineCol > 0 {
		end := l.lineCol
		if end > len(l.lineBuf) {
			end = len(l.lineBuf)
		}
		l.file.Write(l.lineBuf[:end])
		l.file.WriteString("\n")
	}

	footer := fmt.Sprintf(
		"\n# %s\n# Ended   : %s\n# Exit    : %d\n",
		strings.Repeat("-", 68),
		time.Now().Format(time.RFC3339),
		exitCode,
	)
	l.file.WriteString(footer)
	if l.rawFile != nil {
		l.rawFile.Close()
		l.rawFile = nil
	}
	return l.file.Close()
}

// uniquePath returns path unchanged if it does not exist, otherwise appends _1, _2, …
func uniquePath(dir, filename string) string {
	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func generateFilename(args []string) string {
	ts := time.Now().Format("20060102_150405")
	if len(args) == 0 {
		return fmt.Sprintf("%s_unknown.log", ts)
	}

	cmd := args[0]
	var target string

	switch cmd {
	case "ssh":
		raw := extractSSHTarget(args[1:])
		resolved := resolveSSHHostname(raw)
		if idx := strings.LastIndex(resolved, "@"); idx >= 0 {
			target = resolved[idx+1:]
		} else {
			target = resolved
		}
	case "screen":
		target = extractScreenTarget(args[1:])
	default:
		if len(args) > 1 {
			target = sanitize(args[1])
		}
	}

	if target != "" {
		return fmt.Sprintf("%s_%s_%s.log", ts, cmd, sanitize(target))
	}
	return fmt.Sprintf("%s_%s.log", ts, sanitize(cmd))
}

// resolveSSHHostname runs "ssh -G <host>" to resolve an alias defined in
// ~/.ssh/config to its actual HostName. Returns the original target if
// resolution fails or if the hostname is already the canonical name.
func resolveSSHHostname(target string) string {
	if target == "" {
		return target
	}

	user, host := "", target
	if idx := strings.LastIndex(target, "@"); idx >= 0 {
		user = target[:idx+1]
		host = target[idx+1:]
	}

	out, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		return target
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.ToLower(line), "hostname ") {
			resolved := strings.TrimSpace(line[len("hostname "):])
			if resolved != "" && resolved != host {
				return user + resolved
			}
			break
		}
	}
	return target
}

// extractSSHTarget returns the first non-flag argument (user@host or host).
func extractSSHTarget(args []string) string {
	skipNext := false
	// SSH options that consume the next argument
	sshOptionArgs := map[string]bool{
		"-b": true, "-c": true, "-D": true, "-E": true, "-e": true,
		"-F": true, "-I": true, "-i": true, "-J": true, "-L": true,
		"-l": true, "-m": true, "-o": true, "-p": true, "-Q": true,
		"-R": true, "-S": true, "-w": true, "-W": true,
	}
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if sshOptionArgs[arg] {
			skipNext = true
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// extractScreenTarget returns the device basename (e.g. ttyUSB0 from /dev/ttyUSB0).
func extractScreenTarget(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			parts := strings.Split(arg, "/")
			return parts[len(parts)-1]
		}
	}
	return ""
}

func sanitize(s string) string {
	r := strings.NewReplacer(
		"/", "_", ":", "_", " ", "_", "\\", "_",
		"*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	return r.Replace(s)
}

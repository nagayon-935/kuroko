package logger

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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

// IsShellPrompt reports whether p looks like a shell prompt.
func IsShellPrompt(p []byte) bool {
	if len(p) < 2 {
		return false
	}
	// The prompt must start at column 0 (no leading spaces)
	if p[0] == ' ' || p[0] == '\t' {
		return false
	}

	var hasIndicator bool
	var indicatorLen int
	tail := p[len(p)-2:]
	if bytes.Equal(tail, []byte("$ ")) || bytes.Equal(tail, []byte("# ")) || bytes.Equal(tail, []byte("% ")) || bytes.Equal(tail, []byte("> ")) {
		hasIndicator = true
		indicatorLen = 2
	} else {
		// Check for multi-byte prompts like "❯ " or "➔ "
		if bytes.Contains(p, []byte("❯")) {
			idx := bytes.Index(p, []byte("❯"))
			hasIndicator = true
			indicatorLen = len(p) - idx
		} else if bytes.Contains(p, []byte("➔")) {
			idx := bytes.Index(p, []byte("➔"))
			hasIndicator = true
			indicatorLen = len(p) - idx
		}
	}

	if !hasIndicator {
		return false
	}

	prefix := p[:len(p)-indicatorLen]
	trimmedPrefix := bytes.TrimSpace(prefix)
	if len(trimmedPrefix) == 0 {
		return true // Raw prompts like "$ ", "# ", "❯ "
	}
	// Validate characters and structure of prompt prefix.
	// It must not start with a comment character (#) or redirection (>) or dot.
	if trimmedPrefix[0] == '#' || trimmedPrefix[0] == '>' || trimmedPrefix[0] == '.' {
		return false
	}
	for _, b := range trimmedPrefix {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
			continue
		}
		switch b {
		case '_', '-', '.', '@', ':', '~', '/', '[', ']', '(', ')', '+', ' ':
			continue
		default:
			return false
		}
	}

	// If indicator is "$ ", "% ", or "> ", require at least one prompt-specific character
	// to prevent false positives from commands like "echo $ " or "cat $ " or "git branch $ ".
	isCommonIndicator := bytes.Equal(tail, []byte("$ ")) || bytes.Equal(tail, []byte("% ")) || bytes.Equal(tail, []byte("> "))
	if isCommonIndicator {
		if !bytes.Contains(trimmedPrefix, []byte("@")) && !bytes.Contains(trimmedPrefix, []byte(":")) &&
			!bytes.Contains(trimmedPrefix, []byte("~")) && !bytes.Contains(trimmedPrefix, []byte("/")) &&
			!bytes.Contains(trimmedPrefix, []byte("[")) && !bytes.Contains(trimmedPrefix, []byte("]")) &&
			!bytes.Contains(trimmedPrefix, []byte("(")) && !bytes.Contains(trimmedPrefix, []byte(")")) {
			return false
		}
	}

	// Check if the trimmed prefix contains spaces.
	if bytes.Contains(trimmedPrefix, []byte(" ")) {
		// If it contains spaces, it must either:
		// - start with '(' or '['
		// - or contain prompt-specific characters: '@', ':', '~', '/', '[', ']'
		if !bytes.HasPrefix(trimmedPrefix, []byte("(")) && !bytes.HasPrefix(trimmedPrefix, []byte("[")) &&
			!bytes.Contains(trimmedPrefix, []byte("@")) && !bytes.Contains(trimmedPrefix, []byte(":")) &&
			!bytes.Contains(trimmedPrefix, []byte("~")) && !bytes.Contains(trimmedPrefix, []byte("/")) &&
			!bytes.Contains(trimmedPrefix, []byte("[")) && !bytes.Contains(trimmedPrefix, []byte("]")) {
			return false
		}
	}
	return true
}

// SplitPrompt checks if line starts with a valid shell prompt prefix.
// If so, it returns the prompt prefix and the remaining command.
// Otherwise, it returns nil, nil.
func SplitPrompt(line []byte) ([]byte, []byte) {
	for i := 1; i <= len(line); i++ {
		if IsShellPrompt(line[:i]) {
			return line[:i], line[i:]
		}
	}
	return nil, nil
}

// Logger writes session output to a file, applying ANSI stripping and a
// terminal line-buffer simulation so the log is human-readable plain text.
type Logger struct {
	file             *os.File
	rawFile          *os.File // non-nil when KUROKO_RAW_DEBUG=1; receives raw PTY bytes
	Path             string
	altScreen        bool   // true while a full-screen app (vim/less) owns the terminal
	seqBuf           []byte // incomplete escape sequence carried across Write calls
	lineBuf          []byte // current output line being assembled
	lineCol          int    // cursor column within lineBuf
	pendingCR        bool   // \r was the last control char seen; defer reset until next char
	savedLine        []byte // lineBuf snapshot taken at the first bare \r before any overwrite
	storedPrompt     []byte // last verified shell prompt; used to recover from readline history contamination
	writeSeq         uint64 // monotonic counter for raw debug chunks
	redactionEnabled bool   // true if credentials should be redacted
	inPEMBlock       bool   // state for tracking multi-line PEM blocks
	inEsc            bool   // true if currently parsing an ANSI escape sequence
	escBuf           []byte // buffer for the current escape sequence
	networkMode      bool   // true when wrapping a NW device session (ssh/telnet/screen/…)
	closeOnce        sync.Once
	closeErr         error
}

func New(logDir string, args []string, redactionEnabled bool) (*Logger, error) {
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

	l := &Logger{
		file:             f,
		Path:             path,
		redactionEnabled: redactionEnabled,
		networkMode:      isNetworkSessionCommand(args),
	}

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
				return l.processLine(normalizeLineEndings(data))
			}
			if err := l.processLine(normalizeLineEndings(data[:idx])); err != nil {
				return err
			}
			// Suppress alternate screen output entirely without inserting the warning text.
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
//     deferred until the next printable character arrives.
//   - \b  (0x08) backspace — move cursor one column left
//   - \n  (0x0A) newline    — commit the current line to the file
//
// pendingCR is the key to handling the PTY driver's ONLCR behaviour: the
// driver converts \r\n → \r\r\n, and that sequence is often split across
// two successive write calls (\r at the end of one, \n at the start of the
// next).  By NOT resetting lineCol immediately on \r, the content built up
// in lineBuf survives until the \n commits it.  If a printable char follows
// \r instead (readline overwrite), the deferred reset fires then.
//
// savedLine handles the readline "accept-line" redraw: just before executing
// a command, bash/readline erases the prompt and redraws only the bare command
// text (e.g. "cat foo" instead of "user@host:~$ cat foo").  The first bare \r
// snapshots the full prompt+command into savedLine; if the subsequent overwrite
// produces fewer chars, we commit savedLine so the prompt is preserved.
func (l *Logger) processLine(data []byte) error {
	for _, b := range data {
		if l.inEsc {
			l.escBuf = append(l.escBuf, b)
			if isCompleteEscape(l.escBuf) || len(l.escBuf) > 64 {
				l.inEsc = false
				if len(l.escBuf) <= 64 {
					l.processEscape(l.escBuf)
				}
			}
			continue
		}
		if b == 0x1b {
			if l.pendingCR {
				// Snapshot the current lineBuf just before it gets overwritten,
				// but only if it contains a valid shell prompt.
				if l.lineCol > 0 {
					if prompt, _ := SplitPrompt(l.lineBuf[:l.lineCol]); prompt != nil {
						l.savedLine = append(l.savedLine[:0], l.lineBuf[:l.lineCol]...)
						l.storedPrompt = append(l.storedPrompt[:0], prompt...)
					}
				}
				l.lineCol = 0
				l.pendingCR = false
			}
			l.inEsc = true
			l.escBuf = []byte{b}
			continue
		}

		switch b {
		case '\n':
			// If pendingCR is set the \r was the CR half of a CRLF pair —
			// do not reset lineCol; commit the content that is already there.
			l.pendingCR = false
			end := l.lineCol
			if end > len(l.lineBuf) {
				end = len(l.lineBuf)
			}
			out := l.lineBuf[:end:end]

			var hasCommand bool

			// Try to detect and extract the command and prompt.
			if prompt, cmd := SplitPrompt(out); prompt != nil {
				// Case 1: Shell prompt (bash/zsh/…).
				l.storedPrompt = append(l.storedPrompt[:0], prompt...)
				if len(cmd) > 0 {
					hasCommand = true
				}
			} else if l.networkMode {
				// Case 1b: Network device prompt (Cisco/Arista/Juniper/…).
				// NW devices do not perform readline accept-line redraws, so we
				// detect the prompt directly from the committed line.
				if prompt, cmd, info := SplitPromptInfo(out); info.Kind != KindNone && info.Kind != KindShell {
					l.storedPrompt = append(l.storedPrompt[:0], prompt...)
					if len(cmd) > 0 {
						hasCommand = true
					}
					_ = cmd // cmd content already in out; variable used for hasCommand only
				}
			}
			if !hasCommand && len(l.savedLine) > end {
				// Accept-line redraw happened.
				bare := out
				if len(bare) > 0 {
					if prompt, cmd := SplitPrompt(l.savedLine); prompt != nil {
						// Verify if the command in savedLine matches bare, or savedLine is just the prompt.
						if bytes.Equal(cmd, bare) || len(cmd) == 0 {
							// Case 2: Prompt + command matches bare command, or savedLine is just the prompt.
							out = append(append([]byte{}, prompt...), bare...)
							l.storedPrompt = append(l.storedPrompt[:0], prompt...)
							hasCommand = true
						} else if len(l.storedPrompt) > 0 {
							// Case 3: History contamination (mismatch). Reconstruct from stored prompt.
							out = append(append([]byte{}, l.storedPrompt...), bare...)
							hasCommand = true
						}
					}
				} else {
					out = l.savedLine
				}
			}

			if hasCommand {
				// Write command metadata directly inline before writing the prompt+command.
				metaLine := fmt.Sprintf("# kuroko:cmd:%s\n", time.Now().Format(time.RFC3339))
				if _, err := l.file.WriteString(metaLine); err != nil {
					return err
				}
			}

			if l.redactionEnabled {
				out = l.redact(out)
			}
			if _, err := l.file.Write(append(out, '\n')); err != nil {
				return err
			}
			l.lineBuf = l.lineBuf[:0]
			l.lineCol = 0
			l.savedLine = nil
		case '\r':
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
				// Snapshot the current lineBuf just before it gets overwritten,
				// but only if it contains a valid shell prompt.
				if l.lineCol > 0 {
					if prompt, _ := SplitPrompt(l.lineBuf[:l.lineCol]); prompt != nil {
						l.savedLine = append(l.savedLine[:0], l.lineBuf[:l.lineCol]...)
						l.storedPrompt = append(l.storedPrompt[:0], prompt...)
					}
				}
				l.lineCol = 0
				l.pendingCR = false
			}
			if l.lineCol < len(l.lineBuf) {
				l.lineBuf[l.lineCol] = b
			} else {
				// Pad with spaces if cursor was moved right beyond end of buffer
				for len(l.lineBuf) < l.lineCol {
					l.lineBuf = append(l.lineBuf, ' ')
				}
				l.lineBuf = append(l.lineBuf, b)
			}
			l.lineCol++
		}
	}
	return nil
}

func (l *Logger) processEscape(seq []byte) {
	if len(seq) < 3 || seq[1] != '[' {
		return // Ignore non-CSI sequences (like OSC title or two-char codes)
	}

	final := seq[len(seq)-1]
	paramsStr := string(seq[2 : len(seq)-1])

	// Parse parameter N (default to 1)
	n := 1
	if paramsStr != "" {
		// Clean parameter string from non-digits (like semi-colons or modifiers)
		var val int
		var found bool
		for i := 0; i < len(paramsStr); i++ {
			c := paramsStr[i]
			if c >= '0' && c <= '9' {
				val = val*10 + int(c-'0')
				found = true
			} else {
				if found {
					break
				}
			}
		}
		if found && val > 0 {
			n = val
		}
	}

	switch final {
	case 'K': // Clear line from cursor
		if len(paramsStr) == 0 || paramsStr == "0" {
			if l.lineCol < len(l.lineBuf) {
				l.lineBuf = l.lineBuf[:l.lineCol]
			}
		} else if paramsStr == "2" {
			l.lineBuf = l.lineBuf[:0]
			l.lineCol = 0
		}
	case 'C': // Cursor Forward (Right)
		l.lineCol += n
	case 'D': // Cursor Backward (Left)
		l.lineCol -= n
		if l.lineCol < 0 {
			l.lineCol = 0
		}
	case 'P': // Delete character(s) at cursor
		if l.lineCol < len(l.lineBuf) {
			// Shift remaining characters left by n
			if l.lineCol+n < len(l.lineBuf) {
				copy(l.lineBuf[l.lineCol:], l.lineBuf[l.lineCol+n:])
				l.lineBuf = l.lineBuf[:len(l.lineBuf)-n]
			} else {
				l.lineBuf = l.lineBuf[:l.lineCol]
			}
		}
	}
}

// Close flushes any remaining buffered output, writes the session footer, and
// closes the underlying files. It is safe to call concurrently or more than
// once (e.g. from both the normal exit path and a SIGTERM/SIGHUP handler
// racing to shut down before the process dies) — only the first call does
// the work; later calls return the same result.
func (l *Logger) Close(exitCode int) error {
	l.closeOnce.Do(func() {
		l.closeErr = l.doClose(exitCode)
	})
	return l.closeErr
}

func (l *Logger) doClose(exitCode int) error {
	// Flush any unterminated line; apply the same savedLine validation as processLine.
	end := l.lineCol
	if end > len(l.lineBuf) {
		end = len(l.lineBuf)
	}
	out := l.lineBuf[:end]
	if prompt, _ := SplitPrompt(out); prompt != nil {
		// Already has prompt.
	} else if len(l.savedLine) > end {
		bare := out
		if len(bare) > 0 {
			if prompt, cmd := SplitPrompt(l.savedLine); prompt != nil {
				if bytes.Equal(cmd, bare) || len(cmd) == 0 {
					out = append(append([]byte{}, prompt...), bare...)
				} else if len(l.storedPrompt) > 0 {
					out = append(append([]byte{}, l.storedPrompt...), bare...)
				}
			}
		} else {
			out = l.savedLine
		}
	}
	if len(out) > 0 {
		if l.redactionEnabled {
			out = l.redact(out)
		}
		l.file.Write(out)
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

// TargetName returns the human-readable target name for the given command
// arguments (e.g. hostname for ssh, device for screen, command name otherwise).
// Used by log filename generation.
func TargetName(args []string) string {
	_, hostname := TargetDetails(args)
	return hostname
}

// TargetDetails returns two values:
//   - address: full connection target as typed (e.g. "admin@router-a")
//   - hostname: resolved canonical hostname (e.g. "router-a.dc1.example.jp")
//
// For non-SSH commands both values are identical.
// The banner uses both values so operators see what they typed AND the resolved host.
func TargetDetails(args []string) (address, hostname string) {
	if len(args) == 0 {
		return "", ""
	}
	cmd := args[0]
	switch cmd {
	case "ssh":
		raw := extractSSHTarget(args[1:])   // user@host as typed
		resolved := resolveSSHHostname(raw) // may resolve SSH config alias
		// hostname is the bare host part of the resolved target
		h := resolved
		if idx := strings.LastIndex(h, "@"); idx >= 0 {
			h = h[idx+1:]
		}
		return raw, h
	case "screen":
		t := extractScreenTarget(args[1:])
		return t, t
	default:
		return cmd, cmd
	}
}

func generateFilename(args []string) string {
	ts := time.Now().Format("20060102_150405")
	if len(args) == 0 {
		return fmt.Sprintf("%s_unknown.log", ts)
	}

	cmd := args[0]
	// Use the typed host (address without user@) as the filename component so
	// SSH aliases like "edgeSW03" are preserved instead of being replaced by
	// the resolved IP from ssh -G.
	address, _ := TargetDetails(args)
	host := address
	if idx := strings.LastIndex(host, "@"); idx >= 0 {
		host = host[idx+1:]
	}

	if host != "" && host != cmd {
		return fmt.Sprintf("%s_%s_%s.log", ts, cmd, sanitize(host))
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

// CompressFile compresses the file at srcPath using gzip, deletes the original file,
// and returns the path to the compressed file (.gz).
func CompressFile(srcPath string) (string, error) {
	dstPath := srcPath + ".gz"

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer dstFile.Close()

	gzWriter := gzip.NewWriter(dstFile)
	defer gzWriter.Close()

	if _, err := io.Copy(gzWriter, srcFile); err != nil {
		return "", err
	}

	// Close files explicitly before deleting the source
	gzWriter.Close()
	dstFile.Close()
	srcFile.Close()

	if err := os.Remove(srcPath); err != nil {
		return "", err
	}

	return dstPath, nil
}

// RotateLogs scans the logDir and removes old logs based on age and total directory size.
func RotateLogs(logDir string, maxAgeDays int, maxTotalSizeMB int) error {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return err
	}

	type fileInfo struct {
		path    string
		size    int64
		modTime time.Time
	}

	var files []fileInfo
	now := time.Now()
	maxAge := time.Duration(maxAgeDays) * 24 * time.Hour

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Only rotate .log or .log.gz files
		if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".log.gz") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		path := filepath.Join(logDir, name)
		modTime := info.ModTime()

		// 1. Remove files older than maxAgeDays
		if maxAgeDays > 0 && now.Sub(modTime) > maxAge {
			if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
				fmt.Fprintf(os.Stderr, "\033[33m[kuroko] rotation: failed to remove %s: %v\033[0m\n", path, rerr)
			}
			continue
		}

		files = append(files, fileInfo{
			path:    path,
			size:    info.Size(),
			modTime: modTime,
		})
	}

	if maxTotalSizeMB <= 0 {
		return nil
	}

	// Calculate total size and check if it exceeds the limit
	var totalSize int64
	for _, f := range files {
		totalSize += f.size
	}

	maxTotalSizeBytes := int64(maxTotalSizeMB) * 1024 * 1024
	if totalSize <= maxTotalSizeBytes {
		return nil
	}

	// 2. Sort by modTime (oldest first) and delete until total size is within the limit
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	for _, f := range files {
		if totalSize <= maxTotalSizeBytes {
			break
		}
		if err := os.Remove(f.path); err == nil {
			totalSize -= f.size
		}
	}

	return nil
}

var (
	awsAccessKeyPattern = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	awsSecretKeyPattern = regexp.MustCompile(`(?i)(aws_secret_access_key|secret_key|secret)\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})["']?`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)Bearer\s+([a-zA-Z0-9_\-\.\+\/\\=]+)`)
	sqlPasswordPattern  = regexp.MustCompile(`(?i)identified\s+by\s+'([^']+)'`)
)

func (l *Logger) redact(line []byte) []byte {
	if !l.redactionEnabled {
		return line
	}

	lineStr := string(line)

	// Stateful PEM Block redaction
	if strings.Contains(lineStr, "-----BEGIN") && strings.Contains(lineStr, "PRIVATE KEY-----") {
		l.inPEMBlock = true
		return []byte("[PEM PRIVATE KEY BLOCK STARTED - REDACTED]")
	}
	if l.inPEMBlock {
		if strings.Contains(lineStr, "-----END") && strings.Contains(lineStr, "PRIVATE KEY-----") {
			l.inPEMBlock = false
			return []byte("[PEM PRIVATE KEY BLOCK ENDED - REDACTED]")
		}
		return []byte("[PEM PRIVATE KEY DATA - REDACTED]")
	}

	// AWS Access Key ID
	line = awsAccessKeyPattern.ReplaceAll(line, []byte("[AWS_ACCESS_KEY_REDACTED]"))

	// AWS Secret Access Key
	line = awsSecretKeyPattern.ReplaceAllFunc(line, func(match []byte) []byte {
		parts := awsSecretKeyPattern.FindSubmatch(match)
		if len(parts) > 2 {
			secret := parts[2]
			return bytes.Replace(match, secret, []byte("[AWS_SECRET_KEY_REDACTED]"), 1)
		}
		return match
	})

	// Bearer Token
	line = bearerTokenPattern.ReplaceAllFunc(line, func(match []byte) []byte {
		parts := bearerTokenPattern.FindSubmatch(match)
		if len(parts) > 1 {
			token := parts[1]
			return bytes.Replace(match, token, []byte("[TOKEN_REDACTED]"), 1)
		}
		return match
	})

	// SQL Password
	line = sqlPasswordPattern.ReplaceAllFunc(line, func(match []byte) []byte {
		parts := sqlPasswordPattern.FindSubmatch(match)
		if len(parts) > 1 {
			pass := parts[1]
			return bytes.Replace(match, pass, []byte("[PASSWORD_REDACTED]"), 1)
		}
		return match
	})

	return line
}

// InAltScreen returns true if a full-screen application (like vim) is active.
func (l *Logger) InAltScreen() bool {
	return l.altScreen
}

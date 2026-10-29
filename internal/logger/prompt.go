package logger

import "bytes"

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

// maxPromptLen bounds how far into a line SplitPrompt looks for the end of a
// prompt. Real prompts, even ones showing a long working directory, are far
// shorter. The bound keeps detection cheap on very long output lines
// (minified JSON, base64 dumps), which are scanned on the PTY copy path where
// any delay stalls the user's terminal.
const maxPromptLen = 1024

var (
	heavyArrowIndicator = []byte("❯")
	arrowIndicator      = []byte("➔")
)

// isTwoBytePromptIndicator reports whether tail is one of the two-byte
// prompt endings IsShellPrompt recognises.
func isTwoBytePromptIndicator(tail []byte) bool {
	return len(tail) == 2 && tail[1] == ' ' &&
		(tail[0] == '$' || tail[0] == '#' || tail[0] == '%' || tail[0] == '>')
}

// SplitPrompt checks if line starts with a valid shell prompt prefix.
// If so, it returns the shortest such prompt prefix and the remaining
// command. Otherwise, it returns nil, nil.
//
// It returns the same result as testing IsShellPrompt on every prefix in
// turn, but only tests the prefixes that can match. IsShellPrompt needs an
// indicator: the prefix ends in "$ ", "# ", "% " or "> ", or it contains ❯ or
// ➔. A prefix that ends elsewhere can only match through an arrow it
// contains, and then the result depends only on the text before the first
// arrow, so the shortest prefix past each arrow stands for all the others.
// This keeps the scan linear in the line length instead of quadratic.
func SplitPrompt(line []byte) ([]byte, []byte) {
	head := line[:min(len(line), maxPromptLen)]
	heavyEnd, arrowEnd := -1, -1
	if idx := bytes.Index(head, heavyArrowIndicator); idx >= 0 {
		heavyEnd = idx + len(heavyArrowIndicator)
	}
	if idx := bytes.Index(head, arrowIndicator); idx >= 0 {
		arrowEnd = idx + len(arrowIndicator)
	}
	for i := 2; i <= len(head); i++ {
		if i != heavyEnd && i != arrowEnd && !isTwoBytePromptIndicator(head[i-2:i]) {
			continue
		}
		if IsShellPrompt(head[:i]) {
			return line[:i], line[i:]
		}
	}
	return nil, nil
}

// promptTracker recognises command lines among committed rows. Most rows
// carry their prompt directly, but readline's accept-line redraw erases the
// prompt and reprints only the bare command just before running it, so the
// tracker also remembers the last prompt it saw to restore it.
//
// When the shell marks its prompt with OSC 133 (see terminal.handleOSC), the
// marked prompt is used for that row instead of the heuristics, so prompts
// the heuristics cannot recognise ("λ ", "→ ", custom themes) still produce
// command entries. Rows without marks, such as those from a remote shell
// reached through ssh, fall back to the heuristics.
type promptTracker struct {
	networkMode  bool   // also recognise NW device prompts (ssh/telnet/screen/…)
	savedLine    []byte // prompt+command snapshot taken at the first bare \r before any overwrite
	storedPrompt []byte // last verified shell prompt; used to recover from readline history contamination
	marked       bool   // the current row carries an OSC 133 prompt-end mark
	markedPrompt []byte // the row's text up to that mark: the prompt itself
}

// markPromptEnd records that the current row's text up to the cursor, line,
// is the prompt (OSC 133;B).
func (p *promptTracker) markPromptEnd(line []byte) {
	p.marked = true
	p.markedPrompt = append(p.markedPrompt[:0], line...)
}

// clearMark forgets the current row's prompt mark (a new prompt began).
func (p *promptTracker) clearMark() { p.marked = false }

// classifyMarked is classify for a row whose prompt was marked by OSC 133.
func (p *promptTracker) classifyMarked(out []byte) ([]byte, bool) {
	prompt := p.markedPrompt
	p.storedPrompt = append(p.storedPrompt[:0], prompt...)
	if cmd, ok := bytes.CutPrefix(out, prompt); ok {
		return out, len(bytes.TrimSpace(cmd)) > 0
	}
	if len(out) == 0 {
		return append([]byte{}, prompt...), false
	}
	// readline's accept-line redraw overwrote the prompt with the bare
	// command; put the marked prompt back in front of it.
	return append(append([]byte{}, prompt...), out...), true
}

// snapshot records line as the pre-overwrite row when it carries a prompt.
// It runs on the first escape or printable byte to arrive after a bare \r,
// just before that byte overwrites the row — see terminal.feed for why this
// snapshot is needed to survive readline's accept-line redraw.
func (p *promptTracker) snapshot(line []byte) {
	if prompt, _ := SplitPrompt(line); prompt != nil {
		p.savedLine = append(p.savedLine[:0], line...)
		p.storedPrompt = append(p.storedPrompt[:0], prompt...)
	}
}

// classify returns the text to record for the committed row out, with the
// prompt restored if a redraw erased it, and whether the row is a command.
func (p *promptTracker) classify(out []byte) ([]byte, bool) {
	if p.marked {
		return p.classifyMarked(out)
	}
	var hasCommand bool
	if prompt, cmd := SplitPrompt(out); prompt != nil {
		// Case 1: Shell prompt (bash/zsh/…).
		p.storedPrompt = append(p.storedPrompt[:0], prompt...)
		hasCommand = len(cmd) > 0
	} else if p.networkMode {
		// Case 1b: Network device prompt (Cisco/Arista/Juniper/…).
		// NW devices do not perform readline accept-line redraws, so we
		// detect the prompt directly from the committed line.
		if prompt, cmd, info := SplitPromptInfo(out); info.Kind != KindNone && info.Kind != KindShell {
			p.storedPrompt = append(p.storedPrompt[:0], prompt...)
			hasCommand = len(cmd) > 0
		}
	}
	if !hasCommand {
		out, hasCommand = p.restoreSavedPrompt(out)
	}
	return out, hasCommand
}

// lineCommitted forgets the snapshot and prompt mark of the row that was
// just committed.
func (p *promptTracker) lineCommitted() {
	p.savedLine = nil
	p.marked = false
}

// finalLine returns the text to record for a row left unterminated when the
// session ends, applying the same prompt recovery as classify.
func (p *promptTracker) finalLine(out []byte) []byte {
	if p.marked {
		out, _ = p.classifyMarked(out)
		return out
	}
	if prompt, _ := SplitPrompt(out); prompt == nil {
		out, _ = p.restoreSavedPrompt(out)
	}
	return out
}

// restoreSavedPrompt reconstructs the prompt when a readline accept-line
// redraw overwrote the full prompt+command line with a shorter bare command.
func (p *promptTracker) restoreSavedPrompt(out []byte) ([]byte, bool) {
	if len(p.savedLine) <= len(out) {
		return out, false
	}
	bare := out
	if len(bare) == 0 {
		return p.savedLine, false
	}
	prompt, cmd := SplitPrompt(p.savedLine)
	if prompt == nil {
		return out, false
	}
	// Verify if the command in savedLine matches bare, or savedLine is just the prompt.
	if bytes.Equal(cmd, bare) || len(cmd) == 0 {
		// Case 2: Prompt + command matches bare command, or savedLine is just the prompt.
		p.storedPrompt = append(p.storedPrompt[:0], prompt...)
		return append(append([]byte{}, prompt...), bare...), true
	}
	if len(p.storedPrompt) > 0 {
		// Case 3: History contamination (mismatch). Reconstruct from stored prompt.
		return append(append([]byte{}, p.storedPrompt...), bare...), true
	}
	return out, false
}

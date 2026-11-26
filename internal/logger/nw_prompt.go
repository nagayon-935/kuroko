package logger

import (
	"path/filepath"
)

// PromptKind classifies the kind of prompt detected on a line.
type PromptKind int

const (
	KindNone       PromptKind = iota // not a prompt
	KindShell                        // bash/zsh/fish shell prompt
	KindUser                         // NW device user-mode  (Router>)
	KindPrivileged                   // NW device enable-mode (Router#)
	KindConfig                       // NW device config-mode (Router(config-if)#)
)

// PromptInfo carries structured information extracted from a device prompt line.
type PromptInfo struct {
	Kind     PromptKind
	Hostname string // e.g. "Router", "edge-sw01.dc1", "admin@junos"
	Mode     string // config sub-mode, e.g. "config-if", "config-router"; empty for non-config
}

// nwPromptMatch holds byte offsets of a matched network-device prompt on the
// stack so hot paths do not allocate a submatch slice on the heap.
type nwPromptMatch struct {
	end       int // end of full prompt (past '#' or '>')
	hostEnd   int // end of hostname (starts at 0)
	modeStart int // start of mode inside parens, or -1 if absent
	modeEnd   int // end of mode inside parens, or -1 if absent
	indIdx    int // index of '#' or '>'
}

// scanNWPrompt is a zero-allocation linear-time scanner equivalent to the regex
//
//	^([A-Za-z][A-Za-z0-9._@-]*)(\(([A-Za-z0-9][A-Za-z0-9._-]*)\))?([#>])
//
// (kept as nwPromptRe in nw_prompt_test.go and used as a test oracle).
//
// Backtracking can never help: '(', ')', '#' and '>' belong to neither the
// hostname nor the mode character class, so a shorter run is always followed
// by a class byte that cannot satisfy the next element. Non-ASCII bytes are
// in no class and simply end a run. Because line[0] must be an ASCII letter
// and host bytes exclude ':' and '/', URLs and pure-digit strings never match.
func scanNWPrompt(line []byte) (nwPromptMatch, bool) {
	n := len(line)
	if n == 0 || !isASCIILetter(line[0]) {
		return nwPromptMatch{}, false
	}
	i := 1
	for i < n && isHostByte(line[i]) {
		i++
	}
	hostEnd := i
	if i >= n {
		return nwPromptMatch{}, false
	}
	if c := line[i]; c == '#' || c == '>' {
		return nwPromptMatch{
			end:       i + 1,
			hostEnd:   hostEnd,
			modeStart: -1,
			modeEnd:   -1,
			indIdx:    i,
		}, true
	}
	if line[i] != '(' {
		return nwPromptMatch{}, false
	}
	modeStart := i + 1
	if modeStart >= n || !isASCIIAlnum(line[modeStart]) {
		return nwPromptMatch{}, false
	}
	j := modeStart + 1
	for j < n && isModeByte(line[j]) {
		j++
	}
	// need ')' then '#' or '>'
	if j+1 >= n || line[j] != ')' {
		return nwPromptMatch{}, false
	}
	if c := line[j+1]; c != '#' && c != '>' {
		return nwPromptMatch{}, false
	}
	return nwPromptMatch{
		end:       j + 2,
		hostEnd:   hostEnd,
		modeStart: modeStart,
		modeEnd:   j,
		indIdx:    j + 1,
	}, true
}

// matchNWPrompt returns the same 10-element submatch index slice as
// nwPromptRe.FindSubmatchIndex (unmatched optional groups are -1), or nil.
func matchNWPrompt(line []byte) []int {
	m, ok := scanNWPrompt(line)
	if !ok {
		return nil
	}
	if m.modeStart < 0 {
		return []int{0, m.end, 0, m.hostEnd, -1, -1, -1, -1, m.indIdx, m.end}
	}
	return []int{0, m.end, 0, m.hostEnd, m.hostEnd, m.modeEnd + 1, m.modeStart, m.modeEnd, m.indIdx, m.end}
}

func isASCIILetter(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
func isASCIIAlnum(c byte) bool  { return isASCIILetter(c) || c >= '0' && c <= '9' }
func isModeByte(c byte) bool    { return isASCIIAlnum(c) || c == '.' || c == '_' || c == '-' }
func isHostByte(c byte) bool    { return isModeByte(c) || c == '@' }

// splitNWPrompt extracts a network-device prompt and trailing command without
// allocating strings or running shell-prompt heuristics.
func splitNWPrompt(line []byte) (prompt, cmd []byte, ok bool) {
	m, matched := scanNWPrompt(line)
	if !matched {
		return nil, nil, false
	}
	if m.end < len(line) {
		cmd = line[m.end:]
	}
	return line[:m.end], cmd, true
}

// SplitPromptInfo analyses line and returns:
//   - prompt: the prompt prefix bytes (nil if not detected)
//   - cmd:    the command bytes after the prompt (nil if not detected)
//   - info:   structured PromptInfo (Kind==KindNone if not a recognised prompt)
//
// Detection order:
//  1. SplitPrompt incremental scan (bash/zsh/fish/starship etc.) → KindShell.
//  2. Else try NW device prompt scanner.
func SplitPromptInfo(line []byte) (prompt, cmd []byte, info PromptInfo) {
	if p, c := SplitPrompt(line); p != nil {
		return p, c, PromptInfo{Kind: KindShell}
	}

	m, ok := scanNWPrompt(line)
	if !ok {
		return nil, nil, PromptInfo{}
	}

	hostname := string(line[:m.hostEnd])
	var mode string
	var kind PromptKind
	switch {
	case m.modeStart >= 0:
		mode = string(line[m.modeStart:m.modeEnd])
		kind = KindConfig
	case line[m.indIdx] == '#':
		kind = KindPrivileged
	default:
		kind = KindUser
	}

	promptBytes := line[:m.end]
	var cmdBytes []byte
	if m.end < len(line) {
		cmdBytes = line[m.end:]
	}

	return promptBytes, cmdBytes, PromptInfo{
		Kind:     kind,
		Hostname: hostname,
		Mode:     mode,
	}
}

// isNetworkSessionCommand returns true when the wrapping command suggests
// a network device session (ssh, telnet, screen, cu, minicom).
// This gates NW prompt detection in Logger.processLine so that bare-shell
// sessions (bash, zsh) do not pick up look-alike lines as device prompts.
func isNetworkSessionCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch filepath.Base(args[0]) {
	case "ssh", "telnet", "screen", "cu", "minicom", "picocom":
		return true
	}
	return false
}

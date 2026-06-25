package logger

import (
	"regexp"
	"strings"
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

// nwPromptRe matches Cisco/Arista/NX-OS/Juniper-style prompts.
// Groups: 1=hostname  2=(mode-parens)  3=mode  4=indicator(#/>)
//
// Anchored at start of line; no leading whitespace allowed (checked separately).
// hostname: starts with letter or digit, may contain letters, digits, dots, dashes, underscores,
// and a single @ (Juniper user@host form).
var nwPromptRe = regexp.MustCompile(
	`^([A-Za-z][A-Za-z0-9._@-]*)` + // hostname (must start with letter)
		`(\(([A-Za-z0-9][A-Za-z0-9._-]*)\))?` + // optional (mode)
		`([#>])`) // indicator — immediately after hostname or (mode)

// SplitPromptInfo analyses line and returns:
//   - prompt: the prompt prefix bytes (nil if not detected)
//   - cmd:    the command bytes after the prompt (nil if not detected)
//   - info:   structured PromptInfo (Kind==KindNone if not a recognised prompt)
//
// Detection order:
//  1. SplitPrompt incremental scan (bash/zsh/fish/starship etc.) → KindShell.
//  2. Else try NW device prompt regex.
func SplitPromptInfo(line []byte) (prompt, cmd []byte, info PromptInfo) {
	// 1. Existing shell-prompt detection via incremental prefix scan.
	// SplitPrompt handles lines with trailing commands ("user@host:~$ ls")
	// correctly because it scans from the left, unlike IsShellPrompt which
	// checks the full line ending.
	if p, c := SplitPrompt(line); p != nil {
		return p, c, PromptInfo{Kind: KindShell}
	}

	// 2. NW device prompt
	// Bail out fast on lines that cannot be NW prompts.
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '!' {
		return nil, nil, PromptInfo{}
	}
	// Skip kuroko metadata lines.
	if strings.HasPrefix(string(line), "# kuroko:") {
		return nil, nil, PromptInfo{}
	}

	m := nwPromptRe.FindSubmatchIndex(line)
	if m == nil {
		return nil, nil, PromptInfo{}
	}

	// m[0:2] = full match, m[2:4]=hostname, m[4:6]=(mode-parens), m[6:8]=mode, m[8:10]=indicator
	hostname := string(line[m[2]:m[3]])
	mode := ""
	if m[6] >= 0 {
		mode = string(line[m[6]:m[7]])
	}
	indicator := line[m[8]]

	var kind PromptKind
	switch {
	case mode != "":
		kind = KindConfig
	case indicator == '#':
		kind = KindPrivileged
	default:
		kind = KindUser
	}

	// Guard: reject if hostname looks like a URL path or numeric comparison.
	// Hostname must not contain "://" or consist solely of digits.
	if strings.Contains(hostname, "://") {
		return nil, nil, PromptInfo{}
	}
	if isAllDigits(hostname) {
		return nil, nil, PromptInfo{}
	}

	promptEnd := m[1] // end of the full match (after # or >)
	promptBytes := line[:promptEnd]
	var cmdBytes []byte
	if promptEnd < len(line) {
		cmdBytes = line[promptEnd:]
	}

	return promptBytes, cmdBytes, PromptInfo{
		Kind:     kind,
		Hostname: hostname,
		Mode:     mode,
	}
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// isNetworkSessionCommand returns true when the wrapping command suggests
// a network device session (ssh, telnet, screen, cu, minicom).
// This gates NW prompt detection in Logger.processLine so that bare-shell
// sessions (bash, zsh) do not pick up look-alike lines as device prompts.
func isNetworkSessionCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "ssh", "telnet", "screen", "cu", "minicom", "picocom":
		return true
	}
	return false
}

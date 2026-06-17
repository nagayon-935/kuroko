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

// stripANSI removes escape sequences and normalises line endings.
// PTY output uses \r\n; bare \r is a cursor-return with no newline.
func stripANSI(p []byte) []byte {
	clean := ansiEscape.ReplaceAll(p, nil)
	clean = bytes.ReplaceAll(clean, []byte("\r\n"), []byte("\n"))
	clean = bytes.ReplaceAll(clean, []byte("\r"), nil)
	return clean
}

// Logger writes session output to a file.
type Logger struct {
	file *os.File
	Path string
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

	return &Logger{file: f, Path: path}, nil
}

func (l *Logger) Write(p []byte) (int, error) {
	if _, err := l.file.Write(stripANSI(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (l *Logger) Close(exitCode int) error {
	footer := fmt.Sprintf(
		"\n\n# %s\n# Ended   : %s\n# Exit    : %d\n",
		strings.Repeat("-", 68),
		time.Now().Format(time.RFC3339),
		exitCode,
	)
	l.file.WriteString(footer)
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
		target = resolveSSHHostname(raw)
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

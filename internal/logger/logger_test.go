package logger

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestExtractSSHTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"user@host",                  []string{"user@host"},                    "user@host"},
		{"host only",                  []string{"hostname"},                     "hostname"},
		{"-p flag",                    []string{"-p", "2222", "user@host"},     "user@host"},
		{"-i flag",                    []string{"-i", "key.pem", "user@host"},  "user@host"},
		{"-l and host",                []string{"-l", "user", "hostname"},       "hostname"},
		{"multiple flags",             []string{"-p", "22", "-i", "k", "host"}, "host"},
		{"empty",                      []string{},                               ""},
		{"flags only",                 []string{"-v"},                           ""},
		{"-J jump host then target",   []string{"-J", "jump", "user@host"},     "user@host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSSHTarget(tt.args)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractScreenTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"full path",        []string{"/dev/ttyUSB0"},            "ttyUSB0"},
		{"with baud rate",   []string{"/dev/ttyUSB0", "115200"}, "ttyUSB0"},
		{"ttyS0",            []string{"/dev/ttyS0"},              "ttyS0"},
		{"flag then device", []string{"-fn", "/dev/ttyUSB0"},    "ttyUSB0"},
		{"empty",            []string{},                          ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractScreenTarget(tt.args)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user@host",    "user@host"},
		{"/dev/ttyUSB0", "_dev_ttyUSB0"},
		{"host:22",      "host_22"},
		{"a b c",        "a_b_c"},
		{"normal",       "normal"},
		{"a/b\\c",       "a_b_c"},
		{"a*b?c",        "a_b_c"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitize(tt.input)
			if got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateFilename(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "ssh user@host",
			args:         []string{"ssh", "user@hostname"},
			wantContains: []string{"ssh", "hostname"},
			wantAbsent:   []string{"user@"},
		},
		{
			name:         "ssh with -p",
			args:         []string{"ssh", "-p", "2222", "user@host"},
			wantContains: []string{"ssh", "host"},
			wantAbsent:   []string{"user@"},
		},
		{
			name:         "screen device",
			args:         []string{"screen", "/dev/ttyUSB0", "115200"},
			wantContains: []string{"screen", "ttyUSB0"},
		},
		{
			name:         "empty args",
			args:         []string{},
			wantContains: []string{"unknown"},
		},
		{
			name:         "bash",
			args:         []string{"bash"},
			wantContains: []string{"bash"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateFilename(tt.args)
			if !strings.HasSuffix(got, ".log") {
				t.Errorf("filename %q has no .log suffix", got)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("filename %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("filename %q should not contain %q", got, absent)
				}
			}
		})
	}
}

// TestResolveSSHHostname verifies the fallback path (no real ssh -G call).
// We test that passing an already-canonical hostname returns it unchanged.
func TestResolveSSHHostnameFallback(t *testing.T) {
	// A host that is NOT an alias should come back as-is.
	// ssh -G will succeed but return the same hostname.
	got := resolveSSHHostname("localhost")
	if got != "localhost" {
		t.Errorf("resolveSSHHostname(\"localhost\") = %q, want \"localhost\"", got)
	}
}

func TestResolveSSHHostnameEmpty(t *testing.T) {
	if got := resolveSSHHostname(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestUniquePath(t *testing.T) {
	tmp := t.TempDir()

	// First call: file doesn't exist — no suffix.
	p1 := uniquePath(tmp, "session.log")
	if !strings.HasSuffix(p1, "session.log") {
		t.Errorf("first path %q should end with session.log", p1)
	}

	// Create the file so the next call must produce a different name.
	os.WriteFile(p1, []byte{}, 0o600)

	p2 := uniquePath(tmp, "session.log")
	if p2 == p1 {
		t.Errorf("second path should differ from first, got %q", p2)
	}
	if !strings.HasSuffix(p2, "session_1.log") {
		t.Errorf("second path %q should end with session_1.log", p2)
	}

	// Create that too — third should be _2.
	os.WriteFile(p2, []byte{}, 0o600)
	p3 := uniquePath(tmp, "session.log")
	if !strings.HasSuffix(p3, "session_2.log") {
		t.Errorf("third path %q should end with session_2.log", p3)
	}
}

func TestNewLoggerFilePermissions(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "user@host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer l.Close(0)

	info, err := os.Stat(l.Path)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file permissions = %04o, want 0600", perm)
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "color code",
			input: "\x1b[32mhello\x1b[0m",
			want:  "hello",
		},
		{
			name:  "cursor movement",
			input: "\x1b[1;80r\x1b[H\x1b[2Jtext",
			want:  "text",
		},
		{
			name:  "private mode",
			input: "\x1b[?2004houtput\x1b[?2004l",
			want:  "output",
		},
		{
			name:  "OSC title sequence",
			input: "\x1b]0;user@host: ~\x07prompt",
			want:  "prompt",
		},
		{
			name:  "CRLF normalised",
			input: "line1\r\nline2\r\n",
			want:  "line1\nline2\n",
		},
		{
			name:  "bare CR preserved for processLine",
			input: "text\roverwrite",
			want:  "text\roverwrite",
		},
		{
			name:  "doubled CRLF from PTY driver",
			input: "content\r\r\n",
			want:  "content\n",
		},
		{
			name:  "plain text unchanged",
			input: "hello world\n",
			want:  "hello world\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(stripANSI([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("stripANSI(%q)\n got  %q\n want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoggerANSIStripped(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Write content that mimics what a real SSH session produces.
	l.Write([]byte("\x1b[32mWelcome\x1b[0m to server\r\n"))
	l.Write([]byte("\x1b[?2004huser@host:~$ ls\r\n"))
	l.Write([]byte("\x1b[?2004lfile.txt\r\n"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)

	// Escape sequences must be gone.
	if bytes.Contains(data, []byte("\x1b")) {
		t.Errorf("log still contains ESC bytes:\n%q", content)
	}
	// Actual text must survive.
	for _, want := range []string{"Welcome to server", "user@host:~$ ls", "file.txt"} {
		if !strings.Contains(content, want) {
			t.Errorf("log missing %q", want)
		}
	}
}

func TestLoggerCarriageReturn(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// readline-style overwrite: type partial command, \r, then full command
	l.Write([]byte("partial cmd\rfull command\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "full command") {
		t.Errorf("expected 'full command' after CR overwrite, got:\n%s", content)
	}
	// "partial" should not appear as a standalone prefix; overwritten content
	// must not bleed through.
	if strings.Contains(content, "partial cmd") {
		t.Errorf("overwritten 'partial cmd' should not survive in log:\n%s", content)
	}
}

// TestLoggerReadlineAcceptLine reproduces the "cat prompt missing" bug.
//
// bash/readline's accept-line handler erases the prompt and redraws only the
// bare command text (\r + cmd + \r\n) just before executing.  The logger must
// prefer the pre-overwrite snapshot (prompt + command) over the shorter
// post-overwrite content.
func TestLoggerReadlineAcceptLine(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Simulate: prompt + command built up while the user types, then
	// readline redraws with just the command (no prompt) before executing.
	l.Write([]byte("user@host:~$ cat file.txt"))        // interactive echo
	l.Write([]byte("\rcat file.txt\r\r\n"))              // accept-line redraw
	l.Write([]byte("output line\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "user@host:~$ cat file.txt") {
		t.Errorf("prompt should be preserved in log, got:\n%s", content)
	}
	if !strings.Contains(content, "output line") {
		t.Errorf("command output should be in log, got:\n%s", content)
	}
}

func TestLoggerBackspace(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// User typed "helo", hit backspace, then "lo" — result should be "hello"
	l.Write([]byte("helo\x08lo\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "hello") {
		t.Errorf("expected 'hello' after backspace correction, got:\n%s", content)
	}
}

func TestLoggerAltScreenSuppressed(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Simulate: shell prompt, then vim opens (alt screen), then vim closes, then prompt again.
	l.Write([]byte("user@host:~$ vim file.txt\r\n"))
	l.Write([]byte("\x1b[?1049h")) // enter alternate screen
	l.Write([]byte("~\r\n~\r\n\"file.txt\" 0L, 0B\r\n"))
	l.Write([]byte("\x1b[?1049l")) // exit alternate screen
	l.Write([]byte("user@host:~$ \r\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if strings.Contains(content, "\"file.txt\" 0L") {
		t.Errorf("vim screen content should be suppressed, got:\n%s", content)
	}
	if !strings.Contains(content, "vim file.txt") {
		t.Errorf("shell command before vim should be logged, got:\n%s", content)
	}
	if !strings.Contains(content, "full-screen app active") {
		t.Errorf("suppression marker should be logged, got:\n%s", content)
	}
}

func TestLoggerPartialEscapeAtBoundary(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Split \x1b[32m across two writes to simulate PTY buffer boundary.
	l.Write([]byte("text\x1b"))
	l.Write([]byte("[32mhello\x1b[0m\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if bytes.Contains(data, []byte("\x1b")) {
		t.Errorf("ESC byte leaked through partial-sequence boundary:\n%q", content)
	}
	if strings.Contains(content, "[32m") {
		t.Errorf("ANSI parameter bytes leaked:\n%q", content)
	}
	if !strings.Contains(content, "texthello") {
		t.Errorf("text content missing:\n%q", content)
	}
}

// TestLoggerSplitCRLF reproduces the "cat command missing from log" bug.
//
// When the user presses Enter, the PTY emits \r\r\n (ONLCR doubling of \r\n).
// If that sequence is split across two Write calls — \r at the end of one
// write and \r\n (or just \n) at the start of the next — the stray \r was
// previously processed by processLine, resetting lineCol to 0 before the \n
// committed, producing a blank line instead of the command line.
func TestLoggerSplitCRLF(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Write 1: prompt + command + trailing \r  (Enter echo, split from \n)
	// Write 2: \n + first line of command output
	l.Write([]byte("user@host:~$ cat file.txt\r"))
	l.Write([]byte("\ncontents here\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "user@host:~$ cat file.txt") {
		t.Errorf("command line should be logged; got:\n%s", content)
	}
	if !strings.Contains(content, "contents here") {
		t.Errorf("command output should be logged; got:\n%s", content)
	}
}

// TestLoggerSplitDoubleCRLF covers the case where both \r of a \r\r\n end up
// at the tail of one write, with \n starting the next.
func TestLoggerSplitDoubleCRLF(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	l.Write([]byte("user@host:~$ ls\r\r"))
	l.Write([]byte("\nfile.txt\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "user@host:~$ ls") {
		t.Errorf("command line should be logged; got:\n%s", content)
	}
	if !strings.Contains(content, "file.txt") {
		t.Errorf("command output should be logged; got:\n%s", content)
	}
}

func TestLoggerWriteAndClose(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := l.Write([]byte("hello world\n")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err := l.Close(0); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "hello world") {
		t.Errorf("log missing written content, got:\n%s", content)
	}
	if !strings.Contains(content, "Exit    : 0") {
		t.Errorf("log missing exit code footer, got:\n%s", content)
	}
}

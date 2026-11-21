package logger

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestNormalizeLineEndings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "CRLF normalised",
			input: "line1\r\nline2\r\n",
			want:  "line1\nline2\n",
		},
		{
			name:  "bare CR preserved for the terminal",
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
			got := string(normalizeLineEndings([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("normalizeLineEndings(%q)\n got  %q\n want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestIsCompleteEscape covers OSC-terminated and two-char escape sequence detection.
func TestIsCompleteEscape(t *testing.T) {
	tests := []struct {
		name string
		seq  []byte
		want bool
	}{
		{"OSC with BEL", []byte("\x1b]0;title\x07"), true},
		{"OSC with ESC ST", []byte("\x1b]0;title\x1b\\"), true},
		{"OSC with bare ESC is incomplete", []byte("\x1b]0;title\x1b"), false},
		{"OSC incomplete", []byte("\x1b]0;title"), false},
		{"two-char ESC M", []byte("\x1bM"), true},
		{"two-char only ESC", []byte("\x1b"), false},
		{"CSI complete", []byte("\x1b[2J"), true},
		{"CSI incomplete", []byte("\x1b[2"), false},
		{"empty", []byte{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCompleteEscape(tt.seq)
			if got != tt.want {
				t.Errorf("isCompleteEscape(%q) = %v, want %v", tt.seq, got, tt.want)
			}
		})
	}
}

func TestLoggerConsumesWholeEscapeSequences(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{
			name:   "OSC title ended by ST leaves no backslash",
			chunks: []string{"\x1b]0;user@host: ~\x1b\\user@host:~$ ls\r\n"},
			want:   []string{"# kuroko:cmd", "user@host:~$ ls"},
		},
		{
			name: "OSC 7 longer than 64 bytes is consumed whole",
			chunks: []string{"\x1b]7;file://myhost.example.com/home/user/projects/some/deeply/nested/directory/path\x07" +
				"user@host:~$ ls\r\n"},
			want: []string{"# kuroko:cmd", "user@host:~$ ls"},
		},
		{
			name:   "OSC 8 hyperlink keeps only the link text",
			chunks: []string{"\x1b]8;;https://example.com/a/very/long/url/that/goes/on/and/on/for/a/while/more\x1b\\link\x1b]8;;\x1b\\\r\n"},
			want:   []string{"link"},
		},
		{
			name:   "charset designation from tput sgr0",
			chunks: []string{"\x1b[01;32muser@host\x1b(B\x1b[m:~$ ls\r\n"},
			want:   []string{"# kuroko:cmd", "user@host:~$ ls"},
		},
		{
			name:   "ST split across writes",
			chunks: []string{"\x1b]0;title\x1b", "\\out", "put\r\n"},
			want:   []string{"output"},
		},
		{
			name:   "OSC split byte by byte",
			chunks: strings.Split("a\x1b]2;t\x07b\r\n", ""),
			want:   []string{"ab"},
		},
		{
			name:   "ESC inside OSC aborts it and starts a new sequence",
			chunks: []string{"\x1b]0;broken\x1b[32mtext\r\n"},
			want:   []string{"text"},
		},
		{
			name:   "CAN cancels a sequence",
			chunks: []string{"\x1b]0;title\x18text\r\n"},
			want:   []string{"text"},
		},
		{
			name:   "DCS passthrough is consumed",
			chunks: []string{"\x1bPtmux;\x1b\x1b]0;x\x07\x1b\\text\r\n"},
			want:   []string{"text"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withoutTimestamps(logBody(t, []string{"bash"}, tt.chunks...))
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("logged rows = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoggerDropsEscapeSequences(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := New(t.TempDir(), []string{"bash"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.Write([]byte(tt.input + "\r\n")); err != nil {
				t.Fatal(err)
			}
			if err := l.Close(0); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(l.Path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("\x1b")) {
				t.Errorf("log still contains ESC bytes:\n%q", data)
			}
			if !strings.Contains(string(data), "\n"+tt.want+"\n") {
				t.Errorf("log missing line %q:\n%s", tt.want, data)
			}
		})
	}
}

func TestLoggerANSIStripped(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
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

func TestLoggerEscapeSequences(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// 1. Initial prompt printed
	l.Write([]byte("user@host:~$ "))

	// 2. User presses UP arrow, which draws:
	// "cat very_long_command.txt"
	// Then user presses UP arrow again, which edits the line in-place:
	// sends carriage return \r, writes "vim", and clears the rest of the line with \x1b[K
	l.Write([]byte("cat very_long_command.txt"))
	l.Write([]byte("\rvim\x1b[K")) // carriage return + "vim" + clear to end of line

	// 3. User hits Enter (accept-line redraw: \rvim\r\r\n -> becomes \rvim\n)
	l.Write([]byte("\rvim\r\r\n"))
	l.Write([]byte("[alt screen]\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	// Verify that the prompt and command were reconstructed correctly as "user@host:~$ vim"
	if !strings.Contains(content, "user@host:~$ vim") {
		t.Errorf("expected reconstructed prompt 'user@host:~$ vim', got:\n%s", content)
	}
	if strings.Contains(content, "very_long_command") {
		t.Errorf("expected stale command 'very_long_command' to be cleared by ESC K, but it remains:\n%s", content)
	}
	// Verify metadata line is present
	if !strings.Contains(content, "# kuroko:cmd:") {
		t.Errorf("expected metadata line for vim, got:\n%s", content)
	}
}

func TestLoggerPartialEscapeAtBoundary(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
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

func TestLoggerCSIOverflowAndPrivateParams(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Huge CSI repeat counts must not overflow int into negative indices or allocate GBs.
	l.Write([]byte("a\x1b[9223372036854775807Cb\n"))
	l.Write([]byte("cde\x1b[2D\x1b[9223372036854775807P\n"))
	l.Write([]byte("row1\nrow2\x1b[1A\x1b[9223372036854775807Brow3\n"))
	// Private/intermediate CSI sequences ending in C/D/P/K/J must be ignored.
	l.Write([]byte("hello\x1b[?5C\x1b[>2K\x1b[ 1Pworld\n"))
	if err := l.Close(0); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "helloworld") {
		t.Errorf("expected private CSI sequences to leave text intact, got:\n%s", string(data))
	}
}

func FuzzLoggerNeverLogsEscapeBytes(f *testing.F) {
	f.Add([]byte("\x1b]0;t\x1b\\x\r\n"), 3)
	f.Add([]byte("\x1b[01;32mu@h\x1b(B\x1b[m:~$ ls\r\n"), 5)
	f.Add([]byte(promptStart+"λ "+promptEnd+"ls\r\n"), 1)
	f.Add([]byte("\x1bPtmux;\x1b\x1b]0;x\x07\x1b\\t\x1b[?1049hvim\x1b[?1049l\r\n"), 7)
	f.Fuzz(func(t *testing.T, data []byte, split int) {
		l, err := New(t.TempDir(), []string{"bash"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if split > 0 {
			for len(data) > split {
				l.Write(data[:split])
				data = data[split:]
			}
		}
		l.Write(data)
		if err := l.Close(0); err != nil {
			t.Fatal(err)
		}
		logged, err := os.ReadFile(l.Path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(string(logged), 0x1b) {
			t.Fatalf("log contains an ESC byte:\n%q", logged)
		}
	})
}

package logger

import (
	"os"
	"strings"
	"testing"
)

// logBody writes chunks to a fresh Logger for args and returns the logged
// rows between the header and the footer.
func logBody(t *testing.T, args []string, chunks ...string) []string {
	t.Helper()
	l, err := New(t.TempDir(), args, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if _, err := l.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(data), strings.Repeat("-", 68)+"\n\n")
	if !ok {
		t.Fatalf("log has no header:\n%s", data)
	}
	body, _, ok = strings.Cut(body, "\n# "+strings.Repeat("-", 68))
	if !ok {
		t.Fatalf("log has no footer:\n%s", data)
	}
	return strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}

// withoutTimestamps replaces command metadata lines by a fixed marker.
func withoutTimestamps(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		if strings.HasPrefix(r, "# kuroko:cmd:") {
			r = "# kuroko:cmd"
		}
		out[i] = r
	}
	return out
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

const (
	promptStart = "\x1b]133;A\x07"
	promptEnd   = "\x1b]133;B\x07"
)

func TestLoggerUsesOSC133PromptMarks(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{
			name:   "prompt the heuristics cannot recognise",
			chunks: []string{promptStart + "λ " + promptEnd + "ls\r\n", "file.txt\r\n"},
			want:   []string{"# kuroko:cmd", "λ ls", "file.txt"},
		},
		{
			name:   "marks ended by ST",
			chunks: []string{"\x1b]133;A\x1b\\→ \x1b]133;B\x1b\\make\r\n"},
			want:   []string{"# kuroko:cmd", "→ make"},
		},
		{
			name: "readline accept-line redraw restores the marked prompt",
			chunks: []string{
				promptStart + "λ " + promptEnd,
				"vim",
				"\rvim\r\r\n",
			},
			want: []string{"# kuroko:cmd", "λ vim"},
		},
		{
			name:   "empty command is not a command",
			chunks: []string{promptStart + "λ " + promptEnd + "\r\n"},
			want:   []string{"λ "},
		},
		{
			name: "rows without marks fall back to the heuristics",
			chunks: []string{
				promptStart + "λ " + promptEnd + "ssh host\r\n",
				"user@host:~$ uptime\r\n",
				"output λ ls\r\n",
			},
			want: []string{"# kuroko:cmd", "λ ssh host", "# kuroko:cmd", "user@host:~$ uptime", "output λ ls"},
		},
		{
			name:   "unterminated marked row at session end",
			chunks: []string{promptStart + "λ " + promptEnd + "exit"},
			want:   []string{"λ exit"},
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

func TestLoggerWithoutOSC133MissesUnusualPrompt(t *testing.T) {
	// Baseline for TestLoggerUsesOSC133PromptMarks: the same row without
	// marks is not recognised as a command.
	got := withoutTimestamps(logBody(t, []string{"bash"}, "λ ls\r\n"))
	if strings.Join(got, "\n") != "λ ls" {
		t.Fatalf("logged rows = %q, want only the plain row", got)
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

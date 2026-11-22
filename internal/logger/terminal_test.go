package logger

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLoggerCarriageReturn(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
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

// TestLoggerSplitCRLF reproduces the "cat command missing from log" bug.
//
// When the user presses Enter, the PTY emits \r\r\n (ONLCR doubling of \r\n).
// If that sequence is split across two Write calls — \r at the end of one
// write and \r\n (or just \n) at the start of the next — the stray \r was
// previously processed by processLine, resetting lineCol to 0 before the \n
// committed, producing a blank line instead of the command line.
func TestLoggerSplitCRLF(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
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
	l, err := New(tmp, []string{"ssh", "host"}, false)
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

// TestLoggerProcessLineBehaviors is a table-driven test covering ESC[2K, non-CSI
// escape ignore, backspace-after-CR, space padding, and control byte discard.
func TestLoggerProcessLineBehaviors(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []string
		absent []string
	}{
		{
			name:   "ESC[2K clears entire line",
			input:  "hello\x1b[2Kworld\n",
			want:   []string{"world"},
			absent: []string{"hello"},
		},
		{
			name: "non-CSI two-char escape ignored",
			// ESC+M is "Reverse Index" — processEscape returns early, surrounding text survives.
			input: "hello\x1bMworld\n",
			want:  []string{"helloworld"},
		},
		{
			name: "backspace after pendingCR resets col to 0",
			// "hello"\r sets pendingCR; \b clears it and resets lineCol=0 (no decrement
			// since 0 > 0 is false); "world" writes from col 0 → commits "world".
			input: "hello\r\bworld\n",
			want:  []string{"world"},
		},
		{
			name: "cursor-forward pads lineBuf with spaces",
			// "hi" (lineCol=2), ESC[5C → lineCol=7, 'X' triggers 5-space pad → "hi     X"
			input: "hi\x1b[5CX\n",
			want:  []string{"hi     X"},
		},
		{
			name:  "control byte below 0x20 discarded",
			input: "he\x01llo\n",
			want:  []string{"hello"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			l, err := New(tmp, []string{"ssh", "host"}, false)
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			l.Write([]byte(tt.input))
			l.Close(0)

			data, err := os.ReadFile(l.Path)
			if err != nil {
				t.Fatalf("ReadFile %s: %v", l.Path, err)
			}
			content := string(data)
			for _, w := range tt.want {
				if !strings.Contains(content, w) {
					t.Errorf("expected %q in log, got:\n%s", w, content)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(content, a) {
					t.Errorf("expected %q absent from log, got:\n%s", a, content)
				}
			}
		})
	}
}

// TestLoggerEscClearPendingCR tests that processing an escape sequence after \r
// resolves the pendingCR so that a subsequent printable character is written
// at the cursor position updated by the escape sequence rather than resetting it to 0.
func TestLoggerEscClearPendingCR(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// 1. Initial command: prompt + command "cat c9lab" (length: 13 + 9 = 22)
	l.Write([]byte("user@host:~$ cat c9lab"))

	// 2. Carriage return (sets pendingCR)
	l.Write([]byte("\r"))

	// 3. Move cursor right by 13 (to the start of the command "cat c9lab")
	l.Write([]byte("\x1b[13C"))

	// 4. Overwrite "cat" with "vim"
	l.Write([]byte("vim"))

	// Move cursor to the end of the line (6 characters to the right: " c9lab")
	l.Write([]byte("\x1b[6C"))

	// 5. Accept line (Enter)
	l.Write([]byte("\r\r\n"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	content := string(data)
	if !strings.Contains(content, "user@host:~$ vim c9lab") {
		t.Errorf("expected command to be 'user@host:~$ vim c9lab', got:\n%s", content)
	}
	if strings.Contains(content, "vimer@host:~$") {
		t.Errorf("prompt was contaminated: %s", content)
	}
}

func TestScreenClearKeepsRowsAlreadyLogged(t *testing.T) {
	tests := []struct {
		name  string
		clear string
	}{
		{"clear (ncurses)", "\x1b[3J\x1b[H\x1b[2J"},
		{"ctrl+l", "\x1b[H\x1b[2J"},
		{"erase scrollback only", "\x1b[3J"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange / Act
			got := withoutTimestamps(logBody(t, []string{"bash"},
				"user@h:~$ echo important\r\nimportant\r\nuser@h:~$ clear\r\n",
				tt.clear,
				"user@h:~$ echo after\r\nafter\r\n"))

			// Assert
			for _, want := range []string{"user@h:~$ echo important", "important", "user@h:~$ clear", "after"} {
				if !slices.Contains(got, want) {
					t.Errorf("row %q lost after screen clear; log:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

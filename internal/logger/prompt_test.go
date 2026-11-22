package logger

import (
	"strings"
	"testing"
)

func TestSplitPromptBackwardCompat(t *testing.T) {
	prompt, cmd := SplitPrompt([]byte("user@host:~$ ls -la"))
	if string(prompt) != "user@host:~$ " {
		t.Errorf("prompt = %q, want %q", string(prompt), "user@host:~$ ")
	}
	if string(cmd) != "ls -la" {
		t.Errorf("cmd = %q, want %q", string(cmd), "ls -la")
	}

	p, c := SplitPrompt([]byte("just some output"))
	if p != nil || c != nil {
		t.Errorf("expected nil,nil for non-prompt, got %q,%q", string(p), string(c))
	}
}

func TestLoggerPromptValidation(t *testing.T) {
	// 1. Valid prompt shapes
	validPrompts := [][]byte{
		[]byte("user@host:~$ "),
		[]byte("user.name@domain.com:~$ "),
		[]byte("containerlab-vm# "),
		[]byte("[user@host path]$ "),
		[]byte("(venv) user@host:~$ "),
		[]byte("user@host ❯ "),
		[]byte("❯ "),
		[]byte("user@host:~/repo (main) $ "),
	}
	for _, p := range validPrompts {
		if !IsShellPrompt(p) {
			t.Errorf("expected IsShellPrompt(%q) to be true", string(p))
		}
	}

	// 2. Invalid prompt shapes (false positives)
	invalidPrompts := [][]byte{
		[]byte("    ### DCI ###"),
		[]byte("echo $ "),
		[]byte("cat $ "),
		[]byte("git branch $ "),
		[]byte("### "),
		[]byte(" > "),
	}
	for _, p := range invalidPrompts {
		if IsShellPrompt(p) {
			t.Errorf("expected IsShellPrompt(%q) to be false", string(p))
		}
	}
}

// TestIsShellPromptArrow tests the ➔ multi-byte prompt indicator.
func TestIsShellPromptArrow(t *testing.T) {
	valid := [][]byte{
		[]byte("user@host ➔ "),
		[]byte("➔ "),
	}
	for _, p := range valid {
		if !IsShellPrompt(p) {
			t.Errorf("IsShellPrompt(%q) should be true", string(p))
		}
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

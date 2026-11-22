package logger

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestLoggerSameLengthCommandSwap reproduces the "vim/cat order swap" bug.
//
// When two commands share the same display length (prompt+cmd), the accept-line
// \r for the SECOND command must overwrite the stale savedLine from the first,
// even though the lengths are equal.  Using > instead of >= caused the stale
// savedLine to survive, committing the WRONG command to the log.
func TestLoggerSameLengthCommandSwap(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Simulate: user browsed history to "cat file", readline refreshed (bare
	// \r sets savedLine=cat), then user switched to "vim file" (same length).
	// Both commands are deliberately the same byte-length.
	l.Write([]byte("user@host:~$ cat file"))   // readline echo — 21 chars
	l.Write([]byte("\r"))                      // Ctrl-U / readline refresh \r → savedLine = cat cmd
	l.Write([]byte("user@host:~$ vim file"))   // readline redraws with vim cmd
	l.Write([]byte("\ruser@host:~$ vim file")) // vim accept-line redraw
	l.Write([]byte("\r\r\n"))                  // accept-line CRLF (ONLCR)
	l.Write([]byte("[vim output]\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "user@host:~$ vim file") {
		t.Errorf("vim command should be in log, got:\n%s", content)
	}
	if strings.Contains(content, "user@host:~$ cat file") {
		t.Errorf("stale cat command must not appear in log, got:\n%s", content)
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
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Simulate: prompt + command built up while the user types, then
	// readline redraws with just the command (no prompt) before executing.
	l.Write([]byte("user@host:~$ cat file.txt")) // interactive echo
	l.Write([]byte("\rcat file.txt\r\r\n"))      // accept-line redraw
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

// TestLoggerReadlineAcceptLineSplit reproduces the "post-vim cat prompt missing"
// bug.  The PTY's ONLCR conversion turns the accept-line \r\n into \r\r\n.
// When that triple-byte sequence is split across writes (\r\r in one, \n in the
// next), the second bare \r was overwriting savedLine with just the command
// (no prompt), so the committed line lost the prompt.
func TestLoggerReadlineAcceptLineSplit(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Build up prompt+command as readline would echo it.
	l.Write([]byte("user@host:~$ cat file.txt")) // interactive echo (P+C chars)
	// accept-line sequence with \r\r\n split across writes:
	//   write 1: \r (accept-line) + command + \r\r (ONLCR artifact, \n not yet)
	//   write 2: \n (arrives in the next read)
	l.Write([]byte("\rcat file.txt\r\r")) // accept-line \r + cmd + trailing \r\r
	l.Write([]byte("\noutput line\n"))    // \n from next write, then command output
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "user@host:~$ cat file.txt") {
		t.Errorf("prompt should be preserved after split ONLCR, got:\n%s", content)
	}
	if !strings.Contains(content, "output line") {
		t.Errorf("command output should be in log, got:\n%s", content)
	}
}

// TestLoggerHistoryContaminationSwap reproduces the vim/cat label swap that
// occurs when readline history browsing renders text from a different command
// (e.g. "cat") into lineBuf just before the accept-line \r for "vim".
//
// The contaminated savedLine must NOT win; instead the logger should
// reconstruct prompt+bare_cmd using the stored prompt learned from an earlier
// clean command.
func TestLoggerHistoryContaminationSwap(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// ── Step 1: clean "ll" command (teaches storedPrompt) ──────────────────
	l.Write([]byte("user@host:~$ ll\r\r\n"))
	l.Write([]byte("total 8\nfile.txt\n"))

	// ── Step 2: vim command with history-browsing contamination ─────────────
	// Simulates readline rendering "cat c9lab/..." (from history) into lineBuf
	// before accept-line fires for "vim c9lab/...".
	// After ANSI stripping the history rendering looks like raw text appended
	// to whatever the prompt wrote — hence the garbage prefix below.
	l.Write([]byte("user@host:~$ cat c9lab/x.yml")) // history display artifact
	l.Write([]byte("\r"))                           // accept-line \r → savedLine = "...cat..." (garbage)
	l.Write([]byte("vim c9lab/x.yml"))              // bare cmd overwrites from col 0
	l.Write([]byte("\r\r\n"))                       // ONLCR CRLF
	l.Write([]byte("[full-screen app active — output suppressed]\n"))

	// ── Step 3: cat command with history-browsing contamination ─────────────
	l.Write([]byte("user@host:~$ vim c9lab/x.yml")) // history display artifact (vim from prev)
	l.Write([]byte("\r"))                           // accept-line \r → savedLine = "...vim..." (garbage)
	l.Write([]byte("cat c9lab/x.yml"))              // bare cmd overwrites from col 0
	l.Write([]byte("\r\r\n"))                       // ONLCR CRLF
	l.Write([]byte("name: foo\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	// vim label must appear (reconstructed from stored prompt + bare cmd)
	if !strings.Contains(content, "vim c9lab/x.yml") {
		t.Errorf("vim command should appear in log:\n%s", content)
	}
	// cat label must appear
	if !strings.Contains(content, "cat c9lab/x.yml") {
		t.Errorf("cat command should appear in log:\n%s", content)
	}
	// vim must come BEFORE cat in the log (correct chronological order)
	vimIdx := strings.Index(content, "vim c9lab/x.yml")
	catIdx := strings.Index(content, "cat c9lab/x.yml")
	if vimIdx >= catIdx {
		t.Errorf("vim (%d) should appear before cat (%d) in log:\n%s", vimIdx, catIdx, content)
	}
}

func TestLoggerHistoryLengthChange(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// ── Step 1: ユーザーが長いコマンドを表示して履歴から切り替える ──
	l.Write([]byte("user@host:~$ cat very_long_command_name_here.txt")) // 履歴の長いコマンドが描画される
	l.Write([]byte("\r"))                                               // 次のコマンドへの切り替え
	l.Write([]byte("user@host:~$ ll"))                                  // 短いコマンドが上書き描画される
	l.Write([]byte("\ruser@host:~$ ll"))                                // エンターキー押下時の readline accept-line 再描画
	l.Write([]byte("\r\r\n"))                                           // ONLCR CRLF
	l.Write([]byte("file1.txt\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	// 短いコマンド "ll" が実行された際に、プロンプトが正しく復元されていること
	if !strings.Contains(content, "user@host:~$ ll") {
		t.Errorf("prompt should be restored for ll, got:\n%s", content)
	}
	// 古い長いコマンドが誤ってマージされて残っていないこと
	if strings.Contains(content, "very_long_command_name_here.txt") {
		t.Errorf("stale very long command should not remain in log, got:\n%s", content)
	}
}

func TestLoggerNoAcceptLineRedraw(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Type "user@host:~$ ll" directly, followed by CRLF without accept-line redraw
	l.Write([]byte("user@host:~$ ll\r\r\n"))
	l.Write([]byte("total 0\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	// Verify metadata line is present
	if !strings.Contains(content, "# kuroko:cmd:") {
		t.Errorf("expected metadata line for ll, got:\n%s", content)
	}
	// Verify prompt + command line is present
	if !strings.Contains(content, "user@host:~$ ll") {
		t.Errorf("expected 'user@host:~$ ll', got:\n%s", content)
	}
}

func TestLoggerHistoryRecallVim(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// 1. Initial command to learn the prompt
	l.Write([]byte("user@host:~$ ll\r\r\n"))
	l.Write([]byte("total 0\n"))

	// 2. Browse history, rendering "cat file.txt" then "vim"
	l.Write([]byte("user@host:~$ cat file.txt")) // show cat from history
	l.Write([]byte("\r"))                        // navigate history
	l.Write([]byte("vim"))                       // show vim (only cmd written due to redraw optimization/cursor)
	l.Write([]byte("\rvim\r\r\n"))               // accept-line sequence for vim
	l.Write([]byte("[alt screen active]\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	// Verify metadata line is present
	if !strings.Contains(content, "# kuroko:cmd:") {
		t.Errorf("expected metadata line, got:\n%s", content)
	}
	if !strings.Contains(content, "user@host:~$ vim") {
		t.Errorf("expected reconstructed prompt for vim 'user@host:~$ vim', got:\n%s", content)
	}
}

// TestCloseUnterminatedSavedLine tests that Close() restores savedLine when a
// session ends mid-line after an accept-line overwrite (Case 2).
func TestCloseUnterminatedSavedLine(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Build prompt+command in lineBuf, then trigger accept-line \r so savedLine
	// is captured, but never write the terminating \n.
	l.Write([]byte("user@host:~$ cat file.txt"))
	l.Write([]byte("\rcat file.txt")) // savedLine = "user@host:~$ cat file.txt"
	// Close with unterminated line — Close() must use savedLine.
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	content := string(data)
	if !strings.Contains(content, "user@host:~$ cat file.txt") {
		t.Errorf("Close should restore savedLine for unterminated line, got:\n%s", content)
	}
}

// TestCloseHistoryContaminationSavedLine tests that Close() falls back to
// storedPrompt when savedLine contains a mismatched command (Case 3).
func TestCloseHistoryContaminationSavedLine(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Step 1: clean "ll" establishes storedPrompt.
	l.Write([]byte("user@host:~$ ll\r\r\n"))
	l.Write([]byte("total 0\n"))

	// Step 2: history contamination — savedLine carries "cat foo" but the bare
	// command (what user actually ran) is "vim foo".  Close() must reconstruct
	// using storedPrompt + bare instead of the contaminated savedLine.
	l.Write([]byte("user@host:~$ cat foo")) // contamination in lineBuf
	l.Write([]byte("\r"))                   // accept-line \r: savedLine = "user@host:~$ cat foo"
	l.Write([]byte("vim foo"))              // bare cmd overwrites from col 0
	// No \n — Close() handles the unterminated line.
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	content := string(data)
	if !strings.Contains(content, "user@host:~$ vim foo") {
		t.Errorf("Close should reconstruct prompt using storedPrompt, got:\n%s", content)
	}
	if strings.Contains(content, "user@host:~$ cat foo") {
		t.Errorf("contaminated cat cmd must not appear in log, got:\n%s", content)
	}
}

func TestEnterWithCursorInsideLineKeepsWholeCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"backspace moves (bash)", "user@h:~$ echo hello world\b\b\b\b\b\r\n", "user@h:~$ echo hello world"},
		{"CSI D moves", "user@h:~$ echo hello world\x1b[5D\r\n", "user@h:~$ echo hello world"},
		{"left then right", "user@h:~$ echo abc\b\bb\r\n", "user@h:~$ echo abc"},
		{"erase last char with backspace-space-backspace", "user@h:~$ lsx\b \b\r\n", "user@h:~$ ls"},
		{"erase to end after moving left", "user@h:~$ ls -la\b\b\b\b\x1b[K\r\n", "user@h:~$ ls"},
		{"carriage return redraw still ends at the cursor", "user@h:~$ cat foo\rcat foo\r\n", "user@h:~$ cat foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withoutTimestamps(logBody(t, []string{"bash"}, tt.input))
			if !slices.Contains(got, tt.want) {
				t.Errorf("log = %q; want a row %q", got, tt.want)
			}
		})
	}
}

func TestRightPromptStaysOutOfTheCommandLine(t *testing.T) {
	// zsh draws RPROMPT by jumping right (CSI C), printing it, and jumping
	// back (CSI D); it then echoes "e\b" before the typed text. Captured from
	// zsh 5.9 at 60 columns with RPROMPT=[main].
	zshPrompt := "\x1b[0m\x1b[27m\x1b[24m\x1b[Juser@h:~% \x1b[K\x1b[43C[main]\x1b[49D\x1b[?2004he\b"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "zsh, Enter at the end",
			input: zshPrompt + "echo hello world\x1b[?2004l\r\r\nhello world\r\n",
			want:  "user@h:~% echo hello world",
		},
		{
			name:  "zsh, Enter with the cursor moved back",
			input: zshPrompt + "echo hello world\b\b\b\x1b[?2004l\r\r\nhello world\r\n",
			want:  "user@h:~% echo hello world",
		},
		{
			name:  "zsh without RPROMPT, cursor moved back",
			input: "\x1b[Juser@h:~% \x1b[K\x1b[?2004he\becho hello world\b\b\b\x1b[?2004l\r\r\nhello world\r\n",
			want:  "user@h:~% echo hello world",
		},
		{
			name:  "OSC 133 prompt mark after the right prompt",
			input: "\x1b]133;A\x07user@h:~% \x1b[43C[main]\x1b[49D\x1b]133;B\x07echo hi\r\nhi\r\n",
			want:  "user@h:~% echo hi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withoutTimestamps(logBody(t, []string{"zsh"}, tt.input))
			if len(got) < 2 || got[0] != "# kuroko:cmd" || got[1] != tt.want {
				t.Errorf("log = %q; want a command row %q", got, tt.want)
			}
		})
	}
}

package logger

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	l, err := New(tmp, []string{"ssh", "user@host"}, false)
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
	l, err := New(tmp, []string{"ssh", "host"}, false)
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
	l, err := New(tmp, []string{"ssh", "host"}, false)
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
	if strings.Contains(content, "full-screen app active") {
		t.Errorf("suppression marker should not be logged, got:\n%s", content)
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
	l.Write([]byte("\r"))                       // Ctrl-U / readline refresh \r → savedLine = cat cmd
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
	l.Write([]byte("\r"))                            // accept-line \r → savedLine = "...cat..." (garbage)
	l.Write([]byte("vim c9lab/x.yml"))              // bare cmd overwrites from col 0
	l.Write([]byte("\r\r\n"))                        // ONLCR CRLF
	l.Write([]byte("[full-screen app active — output suppressed]\n"))

	// ── Step 3: cat command with history-browsing contamination ─────────────
	l.Write([]byte("user@host:~$ vim c9lab/x.yml")) // history display artifact (vim from prev)
	l.Write([]byte("\r"))                            // accept-line \r → savedLine = "...vim..." (garbage)
	l.Write([]byte("cat c9lab/x.yml"))              // bare cmd overwrites from col 0
	l.Write([]byte("\r\r\n"))                        // ONLCR CRLF
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

func TestLoggerWriteAndClose(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"echo", "hello"}, false)
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

func TestCompressFile(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "test.log")
	content := []byte("hello compression test\n")
	if err := os.WriteFile(srcPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	dstPath, err := CompressFile(srcPath)
	if err != nil {
		t.Fatalf("CompressFile error: %v", err)
	}

	// Verify original file is deleted
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Errorf("original file still exists: %s", srcPath)
	}

	// Verify destination file ends with .gz
	if !strings.HasSuffix(dstPath, ".gz") {
		t.Errorf("expected destination path to end with .gz, got: %s", dstPath)
	}

	// Read and decompress
	f, err := os.Open(dstPath)
	if err != nil {
		t.Fatalf("open compressed file error: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("new gzip reader error: %v", err)
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read decompressed error: %v", err)
	}

	if string(decompressed) != string(content) {
		t.Errorf("decompressed content = %q, want %q", string(decompressed), string(content))
	}
}

func TestRotateLogs(t *testing.T) {
	tmp := t.TempDir()

	// Create 3 log files with different modification times and sizes
	// log1: 10 days old, 100 bytes
	// log2: 5 days old, 200 bytes
	// log3: 1 day old, 300 bytes
	files := []struct {
		name    string
		ageDays int
		size    int
	}{
		{"log1.log", 10, 100},
		{"log2.log", 5, 200},
		{"log3.log.gz", 1, 300},
	}

	now := time.Now()
	for _, f := range files {
		path := filepath.Join(tmp, f.name)
		content := make([]byte, f.size)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		// Change modtime
		mTime := now.Add(-time.Duration(f.ageDays) * 24 * time.Hour)
		if err := os.Chtimes(path, mTime, mTime); err != nil {
			t.Fatalf("Chtimes error: %v", err)
		}
	}

	// Test case 1: Rotate by age (maxAgeDays = 7)
	// log1 (10 days) should be deleted, log2 and log3 should remain.
	if err := RotateLogs(tmp, 7, 0); err != nil {
		t.Fatalf("RotateLogs by age error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmp, "log1.log")); !os.IsNotExist(err) {
		t.Error("log1.log (10 days old) should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(tmp, "log2.log")); err != nil {
		t.Error("log2.log (5 days old) should not have been deleted")
	}
	if _, err := os.Stat(filepath.Join(tmp, "log3.log.gz")); err != nil {
		t.Error("log3.log.gz (1 day old) should not have been deleted")
	}

	// Test case 2: Rotate by size (maxTotalSizeMB = 1 (1MB), but we create a 1.5MB file to trigger it)
	largeContent := make([]byte, 1024*1024*3/2) // 1.5MB
	largePath := filepath.Join(tmp, "log4.log")
	if err := os.WriteFile(largePath, largeContent, 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	mTime := now.Add(-2 * 24 * time.Hour)
	if err := os.Chtimes(largePath, mTime, mTime); err != nil {
		t.Fatalf("Chtimes error: %v", err)
	}

	// Currently in folder:
	// log2.log (5 days old, 200 bytes)
	// log4.log (2 days old, 1.5MB)
	// log3.log.gz (1 day old, 300 bytes)
	// Total size is > 1.5MB, limit is 1MB.
	// log2 (oldest) and log4 (second oldest) should be deleted.
	if err := RotateLogs(tmp, 0, 1); err != nil {
		t.Fatalf("RotateLogs by size error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmp, "log2.log")); !os.IsNotExist(err) {
		t.Error("log2.log (oldest) should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(tmp, "log4.log")); !os.IsNotExist(err) {
		t.Error("log4.log (second oldest, large) should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(tmp, "log3.log.gz")); err != nil {
		t.Error("log3.log.gz (newest) should not have been deleted")
	}
}

func TestLoggerRedaction(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, true) // redactionEnabled = true
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// 1. AWS Access Key
	l.Write([]byte("my key is AKIA1234567890123456 here\n"))

	// 2. AWS Secret Key
	l.Write([]byte("export AWS_SECRET_ACCESS_KEY=\"abcd1234abcd1234abcd1234abcd1234abcd1234\"\n"))

	// 3. Bearer Token
	l.Write([]byte("Authorization: Bearer my_secret_jwt_token_123_abc\n"))

	// 4. SQL Password
	l.Write([]byte("ALTER USER 'ryu' IDENTIFIED BY 'super-secret-password';\n"))

	// 5. PEM Private Key (Stateful Block)
	l.Write([]byte("header info\n"))
	l.Write([]byte("-----BEGIN PRIVATE KEY-----\n"))
	l.Write([]byte("MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDh...\n"))
	l.Write([]byte("-----END PRIVATE KEY-----\n"))
	l.Write([]byte("footer info\n"))

	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	content := string(data)

	// Asserts
	if strings.Contains(content, "AKIA1234567890123456") {
		t.Error("AWS Access Key was not redacted")
	}
	if !strings.Contains(content, "[AWS_ACCESS_KEY_REDACTED]") {
		t.Error("Missing AWS Access Key redacted placeholder")
	}

	if strings.Contains(content, "abcd1234abcd1234abcd1234abcd1234abcd1234") {
		t.Error("AWS Secret Key was not redacted")
	}
	if !strings.Contains(content, "[AWS_SECRET_KEY_REDACTED]") {
		t.Error("Missing AWS Secret Key redacted placeholder")
	}

	if strings.Contains(content, "my_secret_jwt_token_123_abc") {
		t.Error("Bearer Token was not redacted")
	}
	if !strings.Contains(content, "[TOKEN_REDACTED]") {
		t.Error("Missing Bearer Token redacted placeholder")
	}

	if strings.Contains(content, "super-secret-password") {
		t.Error("SQL Password was not redacted")
	}
	if !strings.Contains(content, "[PASSWORD_REDACTED]") {
		t.Error("Missing SQL Password redacted placeholder")
	}

	if strings.Contains(content, "MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDh") {
		t.Error("PEM Private Key data was not redacted")
	}
	if !strings.Contains(content, "[PEM PRIVATE KEY BLOCK STARTED - REDACTED]") {
		t.Error("Missing PEM start placeholder")
	}
	if !strings.Contains(content, "[PEM PRIVATE KEY DATA - REDACTED]") {
		t.Error("Missing PEM data placeholder")
	}
	if !strings.Contains(content, "[PEM PRIVATE KEY BLOCK ENDED - REDACTED]") {
		t.Error("Missing PEM end placeholder")
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
	l.Write([]byte("\r"))                         // navigate history
	l.Write([]byte("vim"))                        // show vim (only cmd written due to redraw optimization/cursor)
	l.Write([]byte("\rvim\r\r\n"))                // accept-line sequence for vim
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

// TestInAltScreen verifies the InAltScreen() accessor reflects alt-screen state.
func TestInAltScreen(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer l.Close(0)

	if l.InAltScreen() {
		t.Error("InAltScreen() should be false initially")
	}
	l.Write([]byte("\x1b[?1049h"))
	if !l.InAltScreen() {
		t.Error("InAltScreen() should be true after entering alt screen")
	}
	l.Write([]byte("\x1b[?1049l"))
	if l.InAltScreen() {
		t.Error("InAltScreen() should be false after exiting alt screen")
	}
}

// TestIsCompleteEscape covers OSC-terminated and two-char escape sequence detection.
func TestIsCompleteEscape(t *testing.T) {
	tests := []struct {
		name string
		seq  []byte
		want bool
	}{
		{"OSC with BEL",       []byte("\x1b]0;title\x07"),    true},
		{"OSC with ESC ST",    []byte("\x1b]0;title\x1b\\"),  true},
		{"OSC incomplete",     []byte("\x1b]0;title"),         false},
		{"two-char ESC M",     []byte("\x1bM"),                true},
		{"two-char only ESC",  []byte("\x1b"),                 false},
		{"CSI complete",       []byte("\x1b[2J"),              true},
		{"CSI incomplete",     []byte("\x1b[2"),               false},
		{"empty",              []byte{},                       false},
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

// TestLoggerCursorMovement covers ESC[C (cursor forward) and ESC[D (cursor backward).
func TestLoggerCursorMovement(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		want        string
		wantAbsent  string
	}{
		{
			name:  "cursor forward then overwrite",
			// "hello", back 3 (col=2), forward 1 (col=3), write "XY" → "helXY"
			input: "hello\x1b[3D\x1b[1CXY\n",
			want:  "helXY",
		},
		{
			name: "cursor backward negative clamp",
			// "hi" (lineCol=2), back 99 → clamped to 0, write 'X' at 0 (lineCol=1),
			// '\n' commits lineBuf[:lineCol=1] = "X".  The 'i' tail must not appear.
			input:      "hi\x1b[99DX\n",
			want:       "X",
			wantAbsent: "Xi",
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
			if !strings.Contains(content, tt.want) {
				t.Errorf("expected %q in log, got:\n%s", tt.want, content)
			}
			if tt.wantAbsent != "" && strings.Contains(content, tt.wantAbsent) {
				t.Errorf("expected %q to be absent from log, got:\n%s", tt.wantAbsent, content)
			}
		})
	}
}

// TestLoggerDeleteChars covers ESC[P (delete character at cursor position).
func TestLoggerDeleteChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "delete one char mid-line",
			// "hello world" (11 chars, lineCol=11), back 5 → lineCol=6 ('w'),
			// delete 1 → lineBuf="hello orld" (10 chars), forward 4 → lineCol=10,
			// '\n' commits lineBuf[:10]="hello orld"
			input: "hello world\x1b[5D\x1b[1P\x1b[4C\n",
			want:  "hello orld",
		},
		{
			name:  "delete beyond end truncates to cursor",
			// "hello world" (11 chars), back 5 → col=6, delete 99 → lineBuf[:6] = "hello "
			input: "hello world\x1b[5D\x1b[99P\n",
			want:  "hello ",
		},
		{
			name:  "delete at end is no-op",
			// "hello", lineCol=5 == len → condition false, no change
			input: "hello\x1b[1P\n",
			want:  "hello",
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
			if !strings.Contains(string(data), tt.want) {
				t.Errorf("expected %q in log, got:\n%s", tt.want, string(data))
			}
		})
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
			name:  "non-CSI two-char escape ignored",
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

// TestLoggerRawDebug verifies that KUROKO_RAW_DEBUG=1 creates a .raw sidecar file.
func TestLoggerRawDebug(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("KUROKO_RAW_DEBUG", "1")

	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	l.Write([]byte("hello\n"))
	l.Close(0)

	rawPath := l.Path + ".raw"
	if _, err := os.Stat(rawPath); errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw debug file %s should exist", rawPath)
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", rawPath, err)
	}
	if !strings.Contains(string(raw), "=== write 1") {
		t.Errorf("raw file should contain write entries, got:\n%s", string(raw))
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
	l.Write([]byte("vim foo"))             // bare cmd overwrites from col 0
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

// TestCloseCursorPastEnd tests that Close() handles lineCol > len(lineBuf) gracefully.
func TestCloseCursorPastEnd(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Move cursor 99 columns right beyond "hi" — lineCol becomes 101, lineBuf len=2.
	// Close() must clamp end to len(lineBuf) = 2 and write "hi".
	l.Write([]byte("hi\x1b[99C"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	if !strings.Contains(string(data), "hi") {
		t.Errorf("expected 'hi' in log when cursor past end, got:\n%s", string(data))
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

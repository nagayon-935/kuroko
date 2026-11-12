package logger

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

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

func TestRedactPEMBlockWithoutEndDoesNotSwallowTheSession(t *testing.T) {
	// Arrange: `head -2` prints the BEGIN line but never the END line.
	input := strings.Join([]string{
		"user@h:~$ head -2 id_rsa",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAABlwAAAAdzc2gtcn",
		"user@h:~$ show version",
		"Cisco IOS XE Software",
		"",
	}, "\n")

	// Act
	got := string(RedactText([]byte(input)))

	// Assert
	if strings.Contains(got, "b3BlbnNzaC1rZXk") {
		t.Errorf("key material leaked:\n%s", got)
	}
	for _, want := range []string{"user@h:~$ show version", "Cisco IOS XE Software"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was redacted after an unterminated PEM block:\n%s", want, got)
		}
	}
}

func TestRedactPEMBlockKeepsRedactingKeyBody(t *testing.T) {
	input := strings.Join([]string{
		"-----BEGIN RSA PRIVATE KEY-----",
		"Proc-Type: 4,ENCRYPTED",
		"DEK-Info: AES-128-CBC,0123456789ABCDEF",
		"",
		"MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDh",
		"exit",
		"-----END RSA PRIVATE KEY-----",
		"after",
	}, "\n")

	got := string(RedactText([]byte(input)))

	for _, secret := range []string{"Proc-Type", "DEK-Info", "MIIEvAIBADANBg", "exit"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q inside a PEM block was not redacted:\n%s", secret, got)
		}
	}
	if !strings.HasSuffix(got, "\nafter") {
		t.Errorf("text after the END line was redacted:\n%s", got)
	}
}

func TestRedactPEMBlockGivesUpAfterLineLimit(t *testing.T) {
	lines := []string{"-----BEGIN PRIVATE KEY-----"}
	for i := 0; i < maxPEMBlockLines+5; i++ {
		lines = append(lines, "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo")
	}
	lines = append(lines, "tail")

	got := strings.Split(string(RedactText([]byte(strings.Join(lines, "\n")))), "\n")

	if got[len(got)-1] != "tail" {
		t.Errorf("last row = %q; want it logged once the block exceeds its line limit", got[len(got)-1])
	}
}

func TestRedactNetworkDeviceSecrets(t *testing.T) {
	tests := []struct {
		line   string
		secret string
	}{
		{"enable secret 5 $1$mERr$hx5rVt7rPNoS4wqbXKX7m0", "$1$mERr$hx5rVt7rPNoS4wqbXKX7m0"},
		{"enable password cisco123", "cisco123"},
		{"username admin privilege 15 secret 9 $9$abcdefgh", "$9$abcdefgh"},
		{"username admin secret sha512 $6$saltsalt$hashhash", "$6$saltsalt$hashhash"},
		{"snmp-server community s3cr3tRO RO", "s3cr3tRO"},
		{"tacacs-server key 7 0822455D0A16", "0822455D0A16"},
		{"radius-server key MyRadiusKey", "MyRadiusKey"},
		{" key-string 7 094F471A1A0A", "094F471A1A0A"},
		{" neighbor 10.0.0.1 password 7 13061E010803", "13061E010803"},
		{" ip ospf message-digest-key 1 md5 7 060506324F41", "060506324F41"},
		{" ip ospf authentication-key OspfKey1", "OspfKey1"},
		{" password 7 02050D480809", "02050D480809"},
		{" key 7 070C285F4D06", "070C285F4D06"},
		{`    encrypted-password "$6$abc$def";`, "$6$abc$def"},
		{`    pre-shared-key ascii-text "$9$xyzxyz";`, "$9$xyzxyz"},
		{`    authentication-key "$9$qrsqrs";`, "$9$qrsqrs"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := string(RedactText([]byte(tt.line)))
			if strings.Contains(got, tt.secret) {
				t.Errorf("RedactText(%q) = %q; secret not masked", tt.line, got)
			}
			if !strings.Contains(got, "[SECRET_REDACTED]") {
				t.Errorf("RedactText(%q) = %q; want a [SECRET_REDACTED] placeholder", tt.line, got)
			}
		})
	}
}

func TestRedactLeavesOrdinaryDeviceOutputAlone(t *testing.T) {
	for _, line := range []string{
		"Router#show ip interface brief",
		"key chain OSPF-KEYS",
		" key 1",
		"Password:",
		"% Bad passwords",
		"interface GigabitEthernet0/1",
	} {
		if got := string(RedactText([]byte(line))); got != line {
			t.Errorf("RedactText(%q) = %q; want unchanged", line, got)
		}
	}
}

// writeFooterlessLog writes a session log that has a header but no
// completion footer and holds no lock — what a killed session, or one still
// running under an older kuroko that did not lock its log, leaves behind —
// and dates it idle ago.
func writeFooterlessLog(t *testing.T, root, name string, size int, idle time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, "ssh", "router")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	header := "# kuroko session log\n# Started : 2020-01-01T00:00:00Z\n# Command : ssh router\n# Target  : router\n# " + strings.Repeat("-", 68) + "\n\n"
	if err := os.WriteFile(path, []byte(header+strings.Repeat("x", size)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-idle)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRotateByAgeRemovesAbandonedLogWithoutFooter(t *testing.T) {
	root := t.TempDir()
	abandoned := writeFooterlessLog(t, root, "20200101_000000_ssh_router.log", 10, 40*24*time.Hour)

	if err := RotateLogs(root, 30, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Error("abandoned footer-less log was not rotated")
	}
}

func TestRotateBySizeTreatsFooterlessLogByIdleTime(t *testing.T) {
	tests := []struct {
		name        string
		idle        time.Duration
		wantRemoved bool
	}{
		{"written recently: may be a running legacy session", 2 * time.Hour, false},
		{"just under the threshold", abandonedLogAge - time.Minute, false},
		{"idle past the threshold: abandoned", abandonedLogAge + time.Hour, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange: one footer-less log alone exceeds the 1 MiB limit, so
			// size rotation must decide whether it may be removed.
			root := t.TempDir()
			path := writeFooterlessLog(t, root, "20200101_000000_ssh_router.log", 2<<20, tt.idle)

			// Act
			if err := RotateLogs(root, 0, 1); err != nil {
				t.Fatal(err)
			}

			// Assert
			_, err := os.Stat(path)
			if removed := os.IsNotExist(err); removed != tt.wantRemoved {
				t.Errorf("removed = %v; want %v", removed, tt.wantRemoved)
			}
		})
	}
}

func TestActiveLog(t *testing.T) {
	root := t.TempDir()
	footerless := writeFooterlessLog(t, root, "a.log", 10, 0)
	closed, err := New(root, []string{"ssh", "closed"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(0); err != nil {
		t.Fatal(err)
	}
	locked, err := New(root, []string{"ssh", "locked"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Close(0)

	tests := []struct {
		name string
		path string
		idle time.Duration
		want bool
	}{
		{"locked by a running session, however old", locked.Path, 365 * 24 * time.Hour, true},
		{"footer-less and recent", footerless, time.Hour, true},
		{"footer-less and idle past the threshold", footerless, abandonedLogAge, false},
		{"closed with a footer", closed.Path, time.Hour, false},
		{"missing file", filepath.Join(root, "missing.log"), time.Hour, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := activeLog(tt.path, tt.idle); got != tt.want {
				t.Errorf("activeLog() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestRotateBySizeSkipsActiveSession(t *testing.T) {
	root := t.TempDir()
	active, err := New(root, []string{"ssh", "active-router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(0)
	if _, err := active.Write([]byte(strings.Repeat("x", 2<<20) + "\n")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(active.Path, old, old); err != nil {
		t.Fatal(err)
	}
	closed, err := New(root, []string{"ssh", "closed-router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Write([]byte(strings.Repeat("y", 2<<20) + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(0); err != nil {
		t.Fatal(err)
	}

	if err := RotateLogs(root, 0, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(active.Path); err != nil {
		t.Error("active session log removed by size rotation")
	}
	if _, err := os.Stat(closed.Path); !os.IsNotExist(err) {
		t.Error("closed log not removed although the total exceeds the limit")
	}
}

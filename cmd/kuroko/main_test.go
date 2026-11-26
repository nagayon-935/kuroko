package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points HOME and the log directory at fresh temporary directories
// so run never reads or writes the developer's own kuroko configuration.
func isolate(t *testing.T) (logDir string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUROKO_LOG_DIR", "")
	t.Setenv("KUROKO_NOTIFIER", "")
	t.Setenv("KUROKO_WEBHOOK_URL", "")
	// Never hand a developer's terminal to the session under test: with
	// stdin not a terminal, sessions run without a PTY or raw mode.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = devNull
	t.Cleanup(func() {
		os.Stdin = stdin
		devNull.Close()
	})
	return t.TempDir()
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunHelpAndVersion(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		code, stdout, _ := runCLI(t, args...)
		if code != 0 || !strings.Contains(stdout, "Usage:") {
			t.Errorf("run(%q) = %d, stdout %q; want usage and 0", args, code, stdout)
		}
	}
	for _, args := range [][]string{{"-v"}, {"--version"}} {
		code, stdout, _ := runCLI(t, args...)
		if code != 0 || stdout != "kuroko "+version+"\n" {
			t.Errorf("run(%q) = %d, stdout %q; want the version and 0", args, code, stdout)
		}
	}
	for _, sub := range []string{"logs", "view", "grep", "export", "migrate", "config", "completion"} {
		code, stdout, stderr := runCLI(t, "help", sub)
		if code != 0 || !strings.Contains(stdout, "usage: kuroko "+sub) || stderr != "" {
			t.Errorf("run(help %s) = %d, stdout %q, stderr %q", sub, code, stdout, stderr)
		}
		code, stdout, stderr = runCLI(t, sub, "--help")
		if code != 0 || stdout != "" || !strings.Contains(stderr, "usage: kuroko "+sub) {
			t.Errorf("run(%s --help) = %d, stdout %q, stderr %q", sub, code, stdout, stderr)
		}
	}
	if code, _, stderr := runCLI(t, "help", "unknown"); code != 1 || !strings.Contains(stderr, "unknown help topic") {
		t.Errorf("run(help unknown) = %d, stderr %q", code, stderr)
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	isolate(t)
	code, _, stderr := runCLI(t, "--no-such-flag")
	if code != 1 || !strings.Contains(stderr, "no-such-flag") {
		t.Fatalf("run(--no-such-flag) = %d, stderr %q; want 1 and the flag named", code, stderr)
	}
}

func TestRunCompletion(t *testing.T) {
	isolate(t)
	code, stdout, _ := runCLI(t, "completion", "bash")
	if code != 0 || !strings.Contains(stdout, "kuroko") {
		t.Fatalf("completion bash = %d, %q", code, stdout)
	}
	if code, _, _ := runCLI(t, "completion", "tcsh"); code != 1 {
		t.Fatalf("completion tcsh = %d; want 1", code)
	}
	if code, _, _ := runCLI(t, "completion", "bash", "extra"); code != 1 {
		t.Fatalf("completion bash extra = %d; want 1", code)
	}
}

func TestRunHiddenCompleteListsSubcommands(t *testing.T) {
	logDir := isolate(t)
	code, stdout, _ := runCLI(t, "-d", logDir, "__complete")
	if code != 0 {
		t.Fatalf("__complete = %d", code)
	}
	for _, want := range []string{"logs", "view", "export"} {
		if !strings.Contains(stdout, want+"\n") {
			t.Errorf("__complete output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestRunHiddenCompleteDelegatesWrappedSSH(t *testing.T) {
	logDir := isolate(t)
	sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host edgeSW03\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runCLI(t, "-d", logDir, "__complete", "ssh")
	if code != 0 || stdout != ":wrapped=1\nedgeSW03\n" {
		t.Fatalf("__complete ssh = %d, %q; want the wrapped directive then the ssh_config host", code, stdout)
	}
}

func TestRunRecordsCommandAndReturnsItsExitCode(t *testing.T) {
	logDir := isolate(t)
	// Without a terminal on stdin the session runs the command directly,
	// still logging its output.
	code, _, _ := runCLI(t, "-d", logDir, "sh", "-c", "echo recorded-output; exit 3")
	if code != 3 {
		t.Fatalf("run(sh ... exit 3) = %d; want 3", code)
	}
	logs, err := filepath.Glob(filepath.Join(logDir, "sh", "*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs = %q, %v; want one log under sh/", logs, err)
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Command : sh -c echo recorded-output; exit 3", "recorded-output", "# Exit    : 3"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log lacks %q:\n%s", want, data)
		}
	}

	// The recorded session can then be exported through the CLI.
	code, stdout, _ := runCLI(t, "-d", logDir, "export", "--last")
	if code != 0 || !strings.Contains(stdout, "recorded-output") {
		t.Fatalf("export --last = %d, %q", code, stdout)
	}
}

func TestRunReportsFailures(t *testing.T) {
	logDir := isolate(t)
	for _, args := range [][]string{
		{"-d", logDir, "view", filepath.Join(logDir, "missing.log")},
		{"-d", logDir, "export", "--bad-flag"},
		{"-d", logDir, "logs", "extra"},
		{"-d", filepath.Join(logDir, "not-a-dir", "x"), "sh", "-c", "true"},
	} {
		if err := os.WriteFile(filepath.Join(logDir, "not-a-dir"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := runCLI(t, args...); code != 1 {
			t.Errorf("run(%q) = %d; want 1", args, code)
		}
	}
}

const testFooter = "\n# --------------------------------------------------------------------\n# Ended   : 2026-06-17T18:00:00+09:00\n# Exit    : 0\n"

func writeTestLog(t *testing.T, logDir, rel, content string) string {
	t.Helper()
	path := filepath.Join(logDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunGrep(t *testing.T) {
	logDir := isolate(t)
	writeTestLog(t, logDir, "ssh/a/20260101_000000_ssh_a.log", "# kuroko:cmd:2026-01-01T00:00:00Z\nInterface DOWN\nup\n")
	writeTestLog(t, logDir, "ssh/b/20260101_000000_ssh_b.log", "all fine\n")

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"match", []string{"grep", "DOWN"}, 0, "ssh/a/20260101_000000_ssh_a.log:2:Interface DOWN\n", ""},
		{"case sensitive miss", []string{"grep", "down"}, 1, "", ""},
		{"ignore case", []string{"grep", "-i", "down"}, 0, "ssh/a/20260101_000000_ssh_a.log:2:Interface DOWN\n", ""},
		{"files only", []string{"grep", "-l", "-i", "down|fine"}, 0, "ssh/a/20260101_000000_ssh_a.log\nssh/b/20260101_000000_ssh_b.log\n", ""},
		{"no match is silent exit 1", []string{"grep", "zzz"}, 1, "", ""},
		{"invalid regexp is exit 2", []string{"grep", "("}, 2, "", "invalid pattern"},
		{"missing pattern is exit 2 with usage", []string{"grep"}, 2, "", "usage: kuroko grep"},
		{"two patterns is exit 2 with usage", []string{"grep", "a", "b"}, 2, "", "usage: kuroko grep"},
		{"unknown flag is exit 2", []string{"grep", "-z", "x"}, 2, "", "-z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"-d", logDir}, tt.args...)
			code, stdout, stderr := runCLI(t, args...)
			if code != tt.wantCode || stdout != tt.wantStdout || !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("run(%q) = %d, %q, %q; want %d, %q, stderr containing %q", args, code, stdout, stderr, tt.wantCode, tt.wantStdout, tt.wantStderr)
			}
			if tt.wantStderr == "" && stderr != "" {
				t.Fatalf("unexpected stderr %q", stderr)
			}
		})
	}
}

func TestRunGrepPrintsFlagAndUsageErrorsOnce(t *testing.T) {
	logDir := isolate(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"grep", "-z", "x"}, "flag provided but not defined"},
		{"missing pattern", []string{"grep"}, "usage: kuroko grep"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, stderr := runCLI(t, append([]string{"-d", logDir}, tt.args...)...)

			if n := strings.Count(stderr, tt.want); n != 1 {
				t.Fatalf("stderr has %q %d times, want once:\n%s", tt.want, n, stderr)
			}
		})
	}
}

func TestRunGrepSkippedUnreadableLogIsExit2EvenWhenOthersMatch(t *testing.T) {
	logDir := isolate(t)
	writeTestLog(t, logDir, "ssh/a/20260101_000000_ssh_a.log.gz", "this is not gzip")
	writeTestLog(t, logDir, "ssh/b/20260101_000000_ssh_b.log", "hit\n")

	code, stdout, stderr := runCLI(t, "-d", logDir, "grep", "hit")

	if code != 2 || stdout != "ssh/b/20260101_000000_ssh_b.log:1:hit\n" || !strings.Contains(stderr, "warning: skipping ssh/a/20260101_000000_ssh_a.log.gz") {
		t.Fatalf("grep = %d, %q, %q; want 2, the match, and a skip warning", code, stdout, stderr)
	}
}

func TestRunGrepHelpExitsZero(t *testing.T) {
	logDir := isolate(t)
	if code, _, stderr := runCLI(t, "-d", logDir, "grep", "-h"); code != 0 || !strings.Contains(stderr, "usage: kuroko grep") {
		t.Fatalf("grep -h = %d, %q", code, stderr)
	}
}

func TestUsageMentionsNewCommands(t *testing.T) {
	isolate(t)
	_, stdout, _ := runCLI(t, "--help")
	for _, want := range []string{"view --follow --last", "grep -i 'error|down'", "completion <bash|zsh|fish>", "one screen"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

func TestUsageMentionsOpenAndLine(t *testing.T) {
	isolate(t)
	_, stdout, _ := runCLI(t, "--help")
	for _, want := range []string{"grep --open", "view --last --line"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
	if !strings.Contains(grepUsage, "--open") {
		t.Error("grep usage lacks --open")
	}
}

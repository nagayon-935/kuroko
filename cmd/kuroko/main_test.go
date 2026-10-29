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

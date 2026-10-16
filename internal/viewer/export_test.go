package viewer

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func saveViewerLog(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if strings.HasSuffix(name, ".gz") {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		if _, err := w.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWholeSessionWithoutCommands(t *testing.T) {
	for _, text := range []string{"ssh: connection refused\n", "", "# kuroko session log\n# Started : 2026-10-08T10:00:00+09:00\n# Command : ssh host\n# ---\n\nlogin banner\n"} {
		path := saveViewerLog(t, t.TempDir(), "plain.log", text)
		v, err := newViewer(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.allCmds) != 1 || !v.allCmds[0].WholeSession {
			t.Fatalf("missing whole session: %+v", v.allCmds)
		}
		if got := strings.Join(v.currentOutputLines, "\n"); got != text {
			t.Fatalf("hidden output: %q; want %q", got, text)
		}
	}
}

func TestWholeSessionIncludesPreambleAndCommandSearch(t *testing.T) {
	text := "connected to router\n" + testLogContent
	path := saveViewerLog(t, t.TempDir(), "commands.log", text)
	v, err := newViewer(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(v.currentOutputLines, "\n"), "connected to router") {
		t.Fatal("connection preamble hidden")
	}
	v.searchQuery = "cat"
	v.updateFilter()
	v.updateOutput()
	if len(v.filteredIdx) != 1 || v.allCmds[v.filteredIdx[0]].Command != "cat file.txt" {
		t.Fatalf("unexpected filter: %v", v.filteredIdx)
	}
	if strings.Contains(strings.Join(v.currentOutputLines, "\n"), "file.txt\n\nuser@") {
		t.Fatal("previous command leaked into output")
	}
	v.searchQuery = ""
	v.updateFilter()
	if len(v.filteredIdx) != 3 || !v.allCmds[v.filteredIdx[0]].WholeSession {
		t.Fatal("whole session not restored")
	}
}

func TestExportCommandsAndSessionPlainAndGzip(t *testing.T) {
	for _, name := range []string{"session.log", "session.log.gz"} {
		t.Run(name, func(t *testing.T) {
			path := saveViewerLog(t, t.TempDir(), name, testLogContent)
			for _, tt := range []struct {
				index int
				want  string
			}{
				{1, "user@host:~$ ls\nfile.txt\n\n"},
				{2, "user@host:~$ cat file.txt\nhello world\n"},
			} {
				var b bytes.Buffer
				if err := Export(path, tt.index, &b); err != nil {
					t.Fatal(err)
				}
				if b.String() != tt.want {
					t.Errorf("command %d=%q; want %q", tt.index, b.String(), tt.want)
				}
			}
			var b bytes.Buffer
			if err := Export(path, 0, &b); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(b.String(), "# Started") || !strings.Contains(b.String(), "hello world") || !strings.Contains(b.String(), "# Exit") || strings.Contains(b.String(), "# kuroko:cmd:") {
				t.Fatalf("bad whole export: %s", b.String())
			}
		})
	}
}

func TestExportOldMetadataAndDeviceCommand(t *testing.T) {
	for _, text := range []string{
		"# kuroko:cmd:2026-10-08T10:00:00+09:00|show version\nRouter#show version\nversion 1\n",
		"# kuroko:cmd:2026-10-08T10:00:00+09:00\nRouter#show version\nversion 1\n",
	} {
		path := saveViewerLog(t, t.TempDir(), "old.log", text)
		var b bytes.Buffer
		if err := Export(path, 1, &b); err != nil {
			t.Fatal(err)
		}
		if b.String() != "Router#show version\nversion 1\n" {
			t.Fatal(b.String())
		}
	}
}

func TestExportToFileDoesNotOverwriteAndCleansErrors(t *testing.T) {
	dir := t.TempDir()
	path := saveViewerLog(t, dir, "source.log", testLogContent)
	output := filepath.Join(dir, "command.txt")
	if err := ExportToFile(path, 1, output); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions=%v", info.Mode())
	}
	original, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExportToFile(path, 2, output); err == nil {
		t.Fatal("overwrote existing export")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("changed existing export: %q %v", after, err)
	}
	if err := ExportToFile(path, 1, path); err == nil {
		t.Fatal("overwrote source log")
	}
	bad := filepath.Join(dir, "bad.txt")
	if err := ExportToFile(path, 99, bad); err == nil {
		t.Fatal("expected invalid command error")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("left partial export: %v", err)
	}
}

func viewerChdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestViewerExportKeepsFullFilteredSelection(t *testing.T) {
	dir := t.TempDir()
	viewerChdir(t, dir)
	longLine := strings.Repeat("機器の出力", 40)
	text := strings.Replace(testLogContent, "hello world", longLine, 1)
	path := saveViewerLog(t, dir, "session.log", text)
	v, err := newViewer(path)
	if err != nil {
		t.Fatal(err)
	}
	v.searchQuery = "cat"
	v.updateFilter()
	v.updateOutput()
	v.outputScroll = 100
	v.outputQuery = "機器"
	v.width = 30
	v.exportSelection()
	v.exportSelection()
	for _, name := range []string{"session_command-002.txt", "session_command-002_1.txt"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), longLine) || !strings.Contains(string(data), "cat file.txt") || strings.Contains(string(data), "\x1b[") {
			t.Fatalf("truncated or highlighted export: %q", data)
		}
	}
	if !strings.Contains(v.statusMessage, "Exported:") {
		t.Fatal(v.statusMessage)
	}
}

func TestViewerLoopExportsWithE(t *testing.T) {
	ptmx, tty := openPTYOrSkip(t)
	defer ptmx.Close()
	defer tty.Close()
	withPTYStdin(t, tty)
	dir := t.TempDir()
	viewerChdir(t, dir)
	path := saveViewerLog(t, dir, "session.log", testLogContent)
	v, err := newViewer(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(200 * time.Millisecond); sendKeys(ptmx, [][]byte{{'j'}, {'e'}, {'q'}}) }()
	done := make(chan error, 1)
	go func() { done <- v.loop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer did not exit")
	}
	data, err := os.ReadFile(filepath.Join(dir, "session_command-001.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user@host:~$ ls\nfile.txt\n\n" {
		t.Fatalf("wrong selection exported: %q", data)
	}
}

func TestPaneWidthsStayWithinTerminal(t *testing.T) {
	for _, width := range []int{1, 2, 3, 20, 40, 80, 120} {
		left, right := paneWidths(width)
		if left < 0 || right < 0 || left+right+1 != width {
			t.Fatalf("width %d: left=%d right=%d", width, left, right)
		}
	}
}

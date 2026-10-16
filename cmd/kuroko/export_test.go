package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "space log.log")
	text := "# kuroko:cmd:2026-10-08T10:00:00+09:00\nRouter#show version\nversion 1\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{path, "--command", "1"}, {"--command=1", path}, {"--last", "--command", "1"}} {
		var stdout, stderr bytes.Buffer
		if err := runExport(args, dir, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != "Router#show version\nversion 1\n" {
			t.Fatalf("export %v=%q", args, stdout.String())
		}
	}
	output := filepath.Join(dir, "export with spaces.txt")
	var stdout, stderr bytes.Buffer
	if err := runExport([]string{path, "--command", "1", "-o", output}, dir, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), output) {
		t.Fatalf("output channels: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if err := runExport([]string{path, "-o", output}, dir, &stdout, &stderr); err == nil {
		t.Fatal("overwrote existing export")
	}
}

func TestRunExportInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}, {"--last", "file.log"}, {"--command", "0", "a"}, {"--command", "-1", "a"}, {"--command", "not-a-number", "a"}, {"--command"}, {"-o"}, {"--unknown"}} {
		var stdout, stderr bytes.Buffer
		if err := runExport(args, t.TempDir(), &stdout, &stderr); err == nil {
			t.Fatalf("accepted %v", args)
		}
		if stdout.Len() != 0 {
			t.Fatalf("partial output for %v", args)
		}
	}
}

func TestRunExportHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runExport([]string{"--help"}, t.TempDir(), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "--command") {
		t.Fatal("missing export help")
	}
}

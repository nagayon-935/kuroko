package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogsInfoDoesNotNeedTerminalOrCreateStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUROKO_LOG_DIR", "")
	root := filepath.Join(home, ".kuroko", "logs")
	var stdout, stderr bytes.Buffer
	if err := runLogs([]string{"--info"}, "", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), root) || !strings.Contains(stdout.String(), "Logs: 0") {
		t.Fatal(stdout.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("--info created storage")
	}
	for _, args := range [][]string{{"--bad"}, {"extra"}} {
		if err := runLogs(args, "", &stdout, &stderr); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestMigrationCommandUsesHomeAndDeletesVerifiedLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := filepath.Join(home, ".config", "kuroko")
	if err := os.MkdirAll(filepath.Join(old, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "logs", "plain.log"), []byte("unstructured log"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runMigration(nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Migration verified: 1 logs") || !strings.Contains(stdout.String(), "legacy config removed") {
		t.Fatal(stdout.String())
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("legacy files retained after verified migration")
	}
	for _, args := range [][]string{{"--bad"}, {"extra"}} {
		if err := runMigration(args, &stdout, &stderr); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

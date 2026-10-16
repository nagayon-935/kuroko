package migration

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logstore"
)

func put(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func logBytes(command, target, output string) []byte {
	return []byte("# kuroko session log\n# Started : 2026-10-16T10:00:00+09:00\n# Command : " + command + "\n# Target  : " + target + "\n# " + strings.Repeat("-", 68) + "\n\n" + output + "\n\n# " + strings.Repeat("-", 68) + "\n# Ended   : 2026-10-16T10:01:00+09:00\n# Exit    : 0\n")
}
func gz(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func expectBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("content verification failed for %s: %v", path, err)
	}
}
func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected removed %s: %v", path, err)
	}
}

func TestMigrationVerifiesMovesAndPreservesSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUROKO_LOG_DIR", "")
	t.Setenv("KUROKO_NOTIFIER", "")
	t.Setenv("KUROKO_WEBHOOK_URL", "")
	old := filepath.Join(home, ".config", "kuroko")
	custom := filepath.Join(home, "work", "logs")
	settings := map[string]any{"log_dir": custom, "notifier": map[string]any{"type": "slack", "webhook_url": "https://example.invalid/secret"}, "storage": map[string]any{"compress_on_close": true}, "redaction": map[string]any{"enabled": true}, "future_setting": map[string]any{"keep": "value"}}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(old, "config.json"), encoded)
	plain := logBytes("ssh -p 2222 admin@edgeSW03", "admin@edgeSW03", "show version")
	compressed := gz(t, logBytes("screen /dev/ttyUSB0", "ttyUSB0", "device output"))
	put(t, filepath.Join(custom, "ssh.log"), plain)
	put(t, filepath.Join(custom, "ssh.log.raw"), []byte("raw bytes"))
	put(t, filepath.Join(custom, "serial.log.gz"), compressed)
	// Earlier default logs must be migrated alongside a later custom root.
	put(t, filepath.Join(old, "logs", "bash.log"), logBytes("bash", "bash", "shell output"))
	when := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(custom, "ssh.log"), when, when); err != nil {
		t.Fatal(err)
	}
	r, err := Run(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Logs != 3 || r.Files != 5 || !r.ConfigMigrated || r.SourceRemaining {
		t.Fatalf("result=%+v", r)
	}
	root := filepath.Join(home, ".kuroko", "logs")
	expectBytes(t, filepath.Join(root, "ssh", "edgeSW03", "ssh.log"), plain)
	expectBytes(t, filepath.Join(root, "ssh", "edgeSW03", "ssh.log.raw"), []byte("raw bytes"))
	expectBytes(t, filepath.Join(root, "screen", "ttyUSB0", "serial.log.gz"), compressed)
	info, err := os.Stat(filepath.Join(root, "ssh", "edgeSW03", "ssh.log"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || !info.ModTime().Equal(when) {
		t.Fatalf("metadata not preserved: %+v", info)
	}
	cfg, err := config.Resolve(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogDir != root || cfg.Notifier.Type != "slack" || cfg.Notifier.WebhookURL != "https://example.invalid/secret" || !cfg.Redaction.Enabled || !cfg.Storage.CompressOnClose {
		t.Fatal("settings changed during migration")
	}
	data, err := os.ReadFile(filepath.Join(home, ".kuroko", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("future_setting")) {
		t.Fatal("unknown settings were discarded")
	}
	absent(t, old)
	absent(t, custom)
	entries, err := logstore.ListLogFiles(root)
	if err != nil || len(entries) != 3 {
		t.Fatalf("migrated logs=%d,%v", len(entries), err)
	}
	r, err = Run(home, "")
	if err != nil || r.Files != 0 {
		t.Fatalf("retry=%+v,%v", r, err)
	}
}

func TestMigrationConflictPreservesExistingConfigAndSources(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".config", "kuroko")
	oldCfg := []byte(`{"notifier":{"type":"none"}}`)
	newCfg := []byte(`{"notifier":{"type":"slack"}}`)
	put(t, filepath.Join(old, "config.json"), oldCfg)
	put(t, filepath.Join(home, ".kuroko", "config.json"), newCfg)
	log := logBytes("bash", "bash", "keep")
	source := filepath.Join(old, "logs", "one.log")
	put(t, source, log)
	if _, err := Run(home, ""); err == nil {
		t.Fatal("accepted conflicting settings")
	}
	expectBytes(t, source, log)
	expectBytes(t, filepath.Join(old, "config.json"), oldCfg)
	expectBytes(t, filepath.Join(home, ".kuroko", "config.json"), newCfg)
}

func TestMigrationCollisionKeepsBothLogs(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".config", "kuroko", "logs")
	a := logBytes("ssh router", "router", "one")
	b := logBytes("ssh router", "router", "two")
	put(t, filepath.Join(old, "a", "same.log"), a)
	put(t, filepath.Join(old, "b", "same.log"), b)
	root := filepath.Join(home, ".kuroko", "logs", "ssh", "router")
	existing := []byte("existing unrelated archive")
	put(t, filepath.Join(root, "same.log"), existing)
	if _, err := Run(home, ""); err != nil {
		t.Fatal(err)
	}
	expectBytes(t, filepath.Join(root, "same.log"), existing)
	expectBytes(t, filepath.Join(root, "same_migrated_1.log"), a)
	expectBytes(t, filepath.Join(root, "same_migrated_2.log"), b)
	absent(t, old)
}

func TestMigrationResumesVerifiedCopy(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".config", "kuroko", "logs")
	data := logBytes("ssh router", "router", "resume")
	put(t, filepath.Join(old, "same.log"), data)
	put(t, filepath.Join(home, ".kuroko", "logs", "ssh", "router", "same.log"), data)
	if _, err := Run(home, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := logstore.ListLogFiles(filepath.Join(home, ".kuroko", "logs"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("retry duplicated log: %v,%v", entries, err)
	}
	absent(t, old)
}

func TestMigrationRejectsCorruptGzipBeforeDeletingAnything(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".config", "kuroko")
	put(t, filepath.Join(old, "config.json"), []byte(`{}`))
	good := logBytes("bash", "bash", "keep")
	put(t, filepath.Join(old, "logs", "a.log"), good)
	put(t, filepath.Join(old, "logs", "b.log.gz"), []byte("invalid archive"))
	if _, err := Run(home, ""); err == nil {
		t.Fatal("accepted corrupt archive")
	}
	expectBytes(t, filepath.Join(old, "logs", "a.log"), good)
	expectBytes(t, filepath.Join(old, "logs", "b.log.gz"), []byte("invalid archive"))
	expectBytes(t, filepath.Join(old, "config.json"), []byte(`{}`))
}

func TestMigrationPreservesUnrelatedFiles(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".config", "kuroko", "logs")
	put(t, filepath.Join(old, "a.log"), []byte("old unstructured log"))
	put(t, filepath.Join(old, "notes.txt"), []byte("user notes"))
	r, err := Run(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.SourceRemaining {
		t.Fatal("did not report retained unrelated files")
	}
	expectBytes(t, filepath.Join(old, "notes.txt"), []byte("user notes"))
	expectBytes(t, filepath.Join(home, ".kuroko", "logs", "unclassified", "a.log"), []byte("old unstructured log"))
}

func TestMigrationRefusesActiveSessionAndSymlink(t *testing.T) {
	for _, kind := range []string{"locked", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			old := filepath.Join(home, ".config", "kuroko", "logs")
			path := filepath.Join(old, "a.log")
			put(t, path, logBytes("bash", "bash", "keep"))
			if kind == "locked" {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			} else {
				real := filepath.Join(home, "outside")
				if err := os.Rename(old, real); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, old); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Run(home, ""); err == nil {
				t.Fatal("migrated active or symlinked source")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("source not preserved")
			}
		})
	}
}

func TestRemovalRechecksSourceAndDestination(t *testing.T) {
	for _, changed := range []string{"source", "destination"} {
		t.Run(changed, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "old.log")
			dst := filepath.Join(dir, "new.log")
			data := []byte("original")
			put(t, src, data)
			snapshot, err := fingerprint(src)
			if err != nil {
				t.Fatal(err)
			}
			p := transfer{source: src, destination: dst, sourceHash: snapshot.hash, targetHash: snapshot.hash, modified: snapshot.modified}
			if err := copyVerified(p); err != nil {
				t.Fatal(err)
			}
			if changed == "source" {
				put(t, src, []byte("new output"))
			} else {
				put(t, dst, []byte("changed destination"))
			}
			if err := removeVerified(p); err == nil {
				t.Fatal("deleted without successful verification")
			}
			if _, err := os.Stat(src); err != nil {
				t.Fatal("source deleted after failed verification")
			}
		})
	}
}

func TestMigrationRefusesAliasedDestinationRoot(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, ".kuroko")
	root := filepath.Join(app, "logs")
	data := logBytes("bash", "bash", "keep")
	put(t, filepath.Join(root, "one.log"), data)
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(app, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(home, filepath.Join(alias, "logs")); err == nil {
		t.Fatal("accepted a source resolving to the destination")
	}
	expectBytes(t, filepath.Join(root, "one.log"), data)
}

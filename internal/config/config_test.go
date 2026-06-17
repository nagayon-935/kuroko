package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", "")
	t.Setenv("KUROKO_NOTIFIER", "")
	t.Setenv("KUROKO_WEBHOOK_URL", "")

	cfg, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	wantLogDir := filepath.Join(tmp, ".config", "kuroko", "logs")
	if cfg.LogDir != wantLogDir {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, wantLogDir)
	}
	if cfg.Notifier.Type != "none" {
		t.Errorf("Notifier.Type = %q, want \"none\"", cfg.Notifier.Type)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	customDir := filepath.Join(tmp, "custom-logs")
	t.Setenv("KUROKO_LOG_DIR", customDir)

	cfg, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LogDir != customDir {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, customDir)
	}
}

func TestLoadFlagOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", filepath.Join(tmp, "env-logs"))

	flagDir := filepath.Join(tmp, "flag-logs")
	cfg, err := Load(Options{LogDir: flagDir})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// flag must win over env var
	if cfg.LogDir != flagDir {
		t.Errorf("LogDir = %q, want flag value %q", cfg.LogDir, flagDir)
	}
}

func TestLoadConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", "")

	configDir := filepath.Join(tmp, ".config", "kuroko")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}

	customLog := filepath.Join(tmp, "my-logs")
	data, _ := json.Marshal(map[string]string{"log_dir": customLog})
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LogDir != customLog {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, customLog)
	}
}

func TestPriorityOrder(t *testing.T) {
	// flag > env > config.json > default
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// config.json
	configDir := filepath.Join(tmp, ".config", "kuroko")
	os.MkdirAll(configDir, 0o700)
	jsonLog := filepath.Join(tmp, "json-logs")
	data, _ := json.Marshal(map[string]string{"log_dir": jsonLog})
	os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600)

	// env var
	envLog := filepath.Join(tmp, "env-logs")
	t.Setenv("KUROKO_LOG_DIR", envLog)

	// flag
	flagLog := filepath.Join(tmp, "flag-logs")

	cfg, err := Load(Options{LogDir: flagLog})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LogDir != flagLog {
		t.Errorf("LogDir = %q, want flag value %q", cfg.LogDir, flagLog)
	}
}

func TestLogDirPermissions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", "")

	cfg, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	info, err := os.Stat(cfg.LogDir)
	if err != nil {
		t.Fatalf("Stat(%q) error: %v", cfg.LogDir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("LogDir permissions = %04o, want 0700", perm)
	}
}

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	wantLogDir := filepath.Join(tmp, ".kuroko", "logs")
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

func TestLoadEnvNotifier(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", "")
	t.Setenv("KUROKO_NOTIFIER", "discord")
	t.Setenv("KUROKO_WEBHOOK_URL", "https://example.com/webhook")

	cfg, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Notifier.Type != "discord" {
		t.Errorf("Notifier.Type = %q; want %q", cfg.Notifier.Type, "discord")
	}
	if cfg.Notifier.WebhookURL != "https://example.com/webhook" {
		t.Errorf("Notifier.WebhookURL = %q; want %q", cfg.Notifier.WebhookURL, "https://example.com/webhook")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", "")

	configDir := filepath.Join(tmp, ".config", "kuroko")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(Options{})
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestResolveDoesNotCreateLogDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", "")
	logDir := filepath.Join(tmp, "absent")

	cfg, err := Resolve(Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	if cfg.LogDir != logDir {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, logDir)
	}
	if _, err := os.Stat(logDir); !os.IsNotExist(err) {
		t.Errorf("Resolve() created %q (Stat err = %v); want it absent", logDir, err)
	}
}

func TestResolveReportsMalformedConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("KUROKO_LOG_DIR", "")
	configDir := filepath.Join(tmp, ".config", "kuroko")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := Resolve(Options{}); err == nil {
		t.Fatal("Resolve() expected error for malformed config.json, got nil")
	}
}

func TestResolveReportsUnreadableConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "kuroko", "config.json")
	// A directory gives a deterministic read failure even when run as root.
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(Options{}); err == nil {
		t.Fatal("Resolve silently ignored an unreadable config")
	}
}

func TestResolvePrefersNewConfigAndKeepsLegacyReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUROKO_LOG_DIR", "")
	t.Setenv("KUROKO_NOTIFIER", "")
	oldDir := filepath.Join(home, ".config", "kuroko")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldConfig := filepath.Join(oldDir, "config.json")
	oldData := []byte(`{"notifier":{"type":"slack"}}`)
	if err := os.WriteFile(oldConfig, oldData, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Resolve(Options{})
	if err != nil || cfg.Notifier.Type != "slack" {
		t.Fatalf("legacy fallback failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".kuroko")); !os.IsNotExist(err) {
		t.Fatal("Resolve performed migration")
	}
	newDir := filepath.Join(home, ".kuroko")
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "config.json"), []byte(`{"notifier":{"type":"discord"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Resolve(Options{})
	if err != nil || cfg.Notifier.Type != "discord" || cfg.LogDir != filepath.Join(newDir, "logs") {
		t.Fatalf("new config not preferred: %v", err)
	}
	data, err := os.ReadFile(oldConfig)
	if err != nil || string(data) != string(oldData) {
		t.Fatal("legacy config changed during resolve")
	}
}

func TestResolveNotifierOptions(t *testing.T) {
	const logEnv = "KUROKO_NOTIFIER_INCLUDE_LOG"
	const argsEnv = "KUROKO_NOTIFIER_INCLUDE_ARGS"
	for _, key := range []string{logEnv, argsEnv} {
		t.Setenv(key, "") // Restore the original environment after the test.
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name, config, env, value string
		wantLog, wantArgs        bool
		wantErr                  bool
	}{
		{"defaults", "{}", "", "", false, false, false},
		{"JSON args", `{"include_args":true}`, "", "", false, true, false},
		{"JSON log", `{"include_log":true}`, "", "", true, false, false},
		{"override log", `{"include_log":true,"include_args":true}`, logEnv, "0", false, true, false},
		{"override args", `{"include_log":true,"include_args":false}`, argsEnv, "TRUE", true, true, false},
		{"invalid log", "{}", logEnv, "sometimes", false, false, true},
		{"invalid args", "{}", argsEnv, "", false, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			paths := PathsForHome(home)
			if err := os.MkdirAll(paths.Directory, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.ConfigFile, []byte(`{"notifier":`+tt.config+"}"), 0600); err != nil {
				t.Fatal(err)
			}
			if tt.env != "" {
				t.Setenv(tt.env, tt.value)
			}

			cfg, err := Resolve(Options{})

			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), tt.env) {
					t.Errorf("Resolve error = %v; want error naming %s", err, tt.env)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Notifier.IncludeLog != tt.wantLog || cfg.Notifier.IncludeArgs != tt.wantArgs {
				t.Errorf("got include_log=%t include_args=%t; want %t, %t", cfg.Notifier.IncludeLog, cfg.Notifier.IncludeArgs, tt.wantLog, tt.wantArgs)
			}
		})
	}
}

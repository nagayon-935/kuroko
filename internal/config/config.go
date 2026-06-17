package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	LogDir   string          `json:"log_dir"`
	Notifier NotifierConfig  `json:"notifier"`
}

type NotifierConfig struct {
	Type       string `json:"type"`        // "none", "discord", "slack"
	WebhookURL string `json:"webhook_url"`
}

// Options are overrides supplied via CLI flags (highest priority).
type Options struct {
	LogDir string // --log-dir / -d
}

// Load reads config from file and environment, then applies opt overrides.
// Priority: CLI flag > env var > config.json > default.
func Load(opt Options) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configDir := filepath.Join(home, ".config", "kuroko")

	cfg := &Config{
		LogDir: filepath.Join(configDir, "logs"),
		Notifier: NotifierConfig{
			Type: "none",
		},
	}

	// 1. config.json
	configFile := filepath.Join(configDir, "config.json")
	if data, err := os.ReadFile(configFile); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	// 2. environment variables
	if v := os.Getenv("KUROKO_LOG_DIR"); v != "" {
		cfg.LogDir = v
	}
	if v := os.Getenv("KUROKO_NOTIFIER"); v != "" {
		cfg.Notifier.Type = v
	}
	if v := os.Getenv("KUROKO_WEBHOOK_URL"); v != "" {
		cfg.Notifier.WebhookURL = v
	}

	// 3. CLI flags (highest priority)
	if opt.LogDir != "" {
		cfg.LogDir = opt.LogDir
	}

	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", cfg.LogDir, err)
	}

	return cfg, nil
}

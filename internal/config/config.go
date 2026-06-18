package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	LogDir    string          `json:"log_dir"`
	Notifier  NotifierConfig  `json:"notifier"`
	Storage   StorageConfig   `json:"storage"`
	Redaction RedactionConfig  `json:"redaction"`
}

type NotifierConfig struct {
	Type       string `json:"type"`        // "none", "discord", "slack"
	WebhookURL string `json:"webhook_url"`
}

type StorageConfig struct {
	CompressOnClose     bool           `json:"compress_on_close"`
	CompressThresholdMB int            `json:"compress_threshold_mb"` // 0 means always compress if compress_on_close is true
	Rotation            RotationConfig `json:"rotation"`
}

type RotationConfig struct {
	Enabled        bool `json:"enabled"`
	MaxAgeDays     int  `json:"max_age_days"`
	MaxTotalSizeMB int  `json:"max_total_size_mb"`
}

type RedactionConfig struct {
	Enabled bool `json:"enabled"`
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
		Storage: StorageConfig{
			CompressOnClose:     false,
			CompressThresholdMB: 0,
			Rotation: RotationConfig{
				Enabled:        false,
				MaxAgeDays:     30,
				MaxTotalSizeMB: 1024,
			},
		},
		Redaction: RedactionConfig{
			Enabled: false,
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

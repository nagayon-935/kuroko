package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/nagayon-935/kuroko/internal/console"
)

type Config struct {
	LogDir    string          `json:"log_dir"`
	Notifier  NotifierConfig  `json:"notifier"`
	Storage   StorageConfig   `json:"storage"`
	Redaction RedactionConfig `json:"redaction"`
	Banner    BannerConfig    `json:"banner"`
}

// BannerConfig controls the session-start banner shown on stderr.
type BannerConfig struct {
	Enabled bool         `json:"enabled"` // default true
	Rules   []BannerRule `json:"rules"`
}

// BannerRule maps a target substring pattern to a label and color.
type BannerRule struct {
	Match string `json:"match"` // substring of target name (case-insensitive)
	Label string `json:"label"` // e.g. "PRODUCTION"
	Color string `json:"color"` // "red","yellow","green","cyan","blue","magenta"
}

type NotifierConfig struct {
	Type        string `json:"type"` // "none", "discord", "slack"
	WebhookURL  string `json:"webhook_url"`
	IncludeLog  bool   `json:"include_log"`
	IncludeArgs bool   `json:"include_args"`
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

// Load resolves the config (see Resolve) and ensures the log directory
// exists, creating it with 0700 permissions if needed.
func Load(opt Options) (*Config, error) {
	cfg, err := Resolve(opt)
	if err != nil {
		return nil, err // already carries the config path
	}

	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", cfg.LogDir, err)
	}

	return cfg, nil
}

// Resolve reads config from file and environment, then applies opt
// overrides, without touching the filesystem beyond reading config.json.
// Priority: CLI flag > env var > config.json > default.
func Resolve(opt Options) (*Config, error) {
	report, err := Inspect(opt)
	if err != nil {
		return nil, err
	}
	for _, issue := range report.Issues {
		console.Warnf("%s", issue)
	}
	return report.Config, nil
}

// Report contains the effective settings, their file source (empty for no file),
// and non-fatal problems discovered during resolution.
type Report struct {
	*Config
	File   string
	Issues []Issue
}

// Inspect resolves the same settings as Resolve without printing warnings.
func Inspect(opt Options) (*Report, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locating home directory: %w", err)
	}

	paths := PathsForHome(home)

	cfg := &Config{
		LogDir: paths.LogDirectory,
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
		Banner: BannerConfig{
			Enabled: true,
		},
	}

	report := &Report{Config: cfg}

	// 1. config.json
	configFile := paths.ConfigFile
	data, err := os.ReadFile(configFile)
	if os.IsNotExist(err) {
		// Keep old settings available until an explicit, verified migration.
		configFile = paths.LegacyConfigFile
		data, err = os.ReadFile(configFile)
		if info, statErr := os.Stat(paths.LegacyLogDirectory); statErr == nil && info.IsDir() {
			cfg.LogDir = paths.LegacyLogDirectory
		}
	}
	if err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config %s: %w", configFile, err)
		}
		report.File = configFile
		report.Issues, err = unknownKeys(data)
		if err != nil {
			return nil, fmt.Errorf("parsing config %s: %w", configFile, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config %s: %w", configFile, err)
	}

	// 2. environment variables
	if v := os.Getenv("KUROKO_LOG_DIR"); v != "" {
		cfg.LogDir = v
	}
	typeSource := "notifier.type"
	if v := os.Getenv("KUROKO_NOTIFIER"); v != "" {
		typeSource = "KUROKO_NOTIFIER"
		cfg.Notifier.Type = v
	}
	if v := os.Getenv("KUROKO_WEBHOOK_URL"); v != "" {
		cfg.Notifier.WebhookURL = v
	}

	for _, override := range []struct {
		name  string
		field *bool
	}{
		{"KUROKO_NOTIFIER_INCLUDE_LOG", &cfg.Notifier.IncludeLog},
		{"KUROKO_NOTIFIER_INCLUDE_ARGS", &cfg.Notifier.IncludeArgs},
	} {
		if v, ok := os.LookupEnv(override.name); ok {
			enabled, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", override.name, err)
			}
			*override.field = enabled
		}
	}

	// 3. CLI flags (highest priority)
	if opt.LogDir != "" {
		cfg.LogDir = opt.LogDir
	}

	if cfg.Notifier.Type == "" {
		cfg.Notifier.Type = "none"
	}
	report.validate(typeSource)
	return report, nil
}

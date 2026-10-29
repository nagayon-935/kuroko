package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/nagayon-935/kuroko/internal/config"
)

type legacySettings struct {
	data   []byte
	values map[string]json.RawMessage
	logDir string
}

func readLegacySettings(path string) (legacySettings, error) {
	data, err := readRegular(path)
	if os.IsNotExist(err) {
		return legacySettings{}, nil
	}
	if err != nil {
		return legacySettings{}, err
	}
	settings := legacySettings{data: data}
	if err := json.Unmarshal(data, &config.Config{}); err != nil {
		return settings, fmt.Errorf("invalid legacy config: %w", err)
	}
	if err := json.Unmarshal(data, &settings.values); err != nil {
		return settings, fmt.Errorf("invalid legacy config: %w", err)
	}
	if settings.values == nil {
		return settings, fmt.Errorf("legacy config must be a JSON object")
	}
	if value, ok := settings.values["log_dir"]; ok {
		if err := json.Unmarshal(value, &settings.logDir); err != nil {
			return settings, fmt.Errorf("invalid legacy log_dir: %w", err)
		}
	}
	return settings, nil
}

func migrationSource(home string, paths config.Paths, configured, override string) (string, error) {
	source := paths.LegacyLogDirectory
	if configured != "" {
		source = configured
	}
	if override != "" {
		if configured != "" && filepath.Clean(configured) != filepath.Clean(override) {
			return source, fmt.Errorf("--from differs from legacy config log_dir; preserved the existing configuration")
		}
		source = override
	}
	if !filepath.IsAbs(source) {
		return source, fmt.Errorf("migration requires an absolute source directory: %s", source)
	}
	source = filepath.Clean(source)
	if overlaps(source, paths.LogDirectory) || source == filepath.Clean(home) || source == string(filepath.Separator) {
		return source, fmt.Errorf("source and destination must be separate log directories")
	}
	return source, nil
}

// planConfig preserves unknown settings and validates conflicts before any
// log is copied. Translating log_dir never mutates the original snapshot.
func planConfig(paths config.Paths, settings legacySettings) (*transfer, error) {
	if settings.values == nil {
		return nil, checkExistingConfig(paths)
	}
	values := make(map[string]json.RawMessage, len(settings.values)+1)
	for key, value := range settings.values {
		values[key] = value
	}
	values["log_dir"], _ = json.Marshal(paths.LogDirectory)
	translated, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return nil, err
	}
	translated = append(translated, '\n')
	if existing, err := readRegular(paths.ConfigFile); err == nil {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(existing, &parsed); err != nil || !sameJSON(parsed, values) {
			return nil, fmt.Errorf("new config conflicts with legacy config; both files have been preserved")
		}
		translated = existing // retain equivalent user formatting
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	info, err := os.Stat(paths.LegacyConfigFile)
	if err != nil {
		return nil, err
	}
	return &transfer{source: paths.LegacyConfigFile, destination: paths.ConfigFile, sourceHash: sha256.Sum256(settings.data), targetHash: sha256.Sum256(translated), modified: info.ModTime(), data: translated}, nil
}

func checkExistingConfig(paths config.Paths) error {
	existing, err := readRegular(paths.ConfigFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg struct {
		LogDir string `json:"log_dir"`
	}
	if err := json.Unmarshal(existing, &cfg); err != nil {
		return err
	}
	if cfg.LogDir != "" && filepath.Clean(cfg.LogDir) != paths.LogDirectory {
		return fmt.Errorf("new config uses a different log directory; preserved existing configuration")
	}
	return nil
}

func sameJSON(a, b map[string]json.RawMessage) bool {
	decode := func(value map[string]json.RawMessage) (any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		// Unknown settings can contain identifiers larger than float64's exact
		// integer range; differing values must still count as a config conflict.
		decoder.UseNumber()
		var decoded any
		err = decoder.Decode(&decoded)
		return decoded, err
	}
	x, err := decode(a)
	if err != nil {
		return false
	}
	y, err := decode(b)
	return err == nil && reflect.DeepEqual(x, y)
}

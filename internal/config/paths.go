package config

import "path/filepath"

// Paths describes both storage layouts without reading or creating files.
// Configuration resolution and migration must agree on these locations.
type Paths struct {
	Directory          string
	ConfigFile         string
	LogDirectory       string
	LegacyDirectory    string
	LegacyConfigFile   string
	LegacyLogDirectory string
}

func PathsForHome(home string) Paths {
	directory := filepath.Join(home, ".kuroko")
	legacy := filepath.Join(home, ".config", "kuroko")
	return Paths{
		Directory:          directory,
		ConfigFile:         filepath.Join(directory, "config.json"),
		LogDirectory:       filepath.Join(directory, "logs"),
		LegacyDirectory:    legacy,
		LegacyConfigFile:   filepath.Join(legacy, "config.json"),
		LegacyLogDirectory: filepath.Join(legacy, "logs"),
	}
}

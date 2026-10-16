// Package migration copies legacy storage, verifies every destination, then
// removes only source files whose content is still identical to the snapshot.
package migration

import (
	"fmt"
	"os"
	"time"

	"github.com/ryu/kuroko/internal/config"
)

type Result struct {
	Source          string
	Destination     string
	Logs            int
	Files           int
	Bytes           int64
	ConfigMigrated  bool
	SourceRemaining bool
}

type transfer struct {
	source, destination    string
	sourceHash, targetHash [32]byte
	modified               time.Time
	data                   []byte // non-nil only for the translated config
}

// Run is explicit: read-only commands and shell completion never trigger a
// migration. A retry accepts identical destinations and preserves conflicts.
func Run(home, sourceOverride string) (Result, error) {
	paths := config.PathsForHome(home)
	result := Result{Source: paths.LegacyLogDirectory, Destination: paths.LogDirectory}
	settings, err := readLegacySettings(paths.LegacyConfigFile)
	if err != nil {
		return result, err
	}
	result.Source, err = migrationSource(home, paths, settings.logDir, sourceOverride)
	if err != nil {
		return result, err
	}
	if err := prepareStorage(paths, result.Source); err != nil {
		return result, err
	}
	lock, err := lockMigration(paths.Directory)
	if err != nil {
		return result, err
	}
	defer lock.Close()

	configTransfer, err := planConfig(paths, settings)
	if err != nil {
		return result, err
	}
	plan, err := planMigration(paths, result.Source, configTransfer)
	if err != nil {
		return result, err
	}
	result.Logs = plan.logs
	if err := plan.execute(&result); err != nil {
		return result, err
	}
	result.SourceRemaining, err = cleanupSources(plan.sources, paths.LegacyDirectory)
	return result, err
}

// execute preserves a global verification barrier: a later failing copy or
// verification must never remove an earlier source, even if its copy is valid.
func (p migrationPlan) execute(result *Result) error {
	for _, file := range p.files {
		if err := copyVerified(file); err != nil {
			return fmt.Errorf("copy verification failed; sources preserved: %w", err)
		}
	}
	for _, file := range p.files {
		if err := verify(file); err != nil {
			return fmt.Errorf("migration verification failed; sources preserved: %w", err)
		}
	}
	for _, file := range p.files {
		if err := removeVerified(file); err != nil {
			return err
		}
		result.Files++
		if file.source == p.configSource {
			result.ConfigMigrated = true
		} else if info, err := os.Stat(file.destination); err == nil {
			result.Bytes += info.Size()
		}
	}
	return nil
}

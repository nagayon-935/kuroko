package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/logger"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

// A plan owns the complete list of sources and destinations before copying.
// All logs (including debug companions) precede the configuration transfer.
type migrationPlan struct {
	sources      []string
	files        []transfer
	logs         int
	configSource string
}

func planMigration(paths config.Paths, source string, configTransfer *transfer) (migrationPlan, error) {
	plan := migrationPlan{sources: []string{source}, configSource: paths.LegacyConfigFile}
	if paths.LegacyLogDirectory != source {
		if _, err := os.Lstat(paths.LegacyLogDirectory); err == nil {
			plan.sources = append(plan.sources, paths.LegacyLogDirectory)
		} else if !os.IsNotExist(err) {
			return plan, err
		}
	}
	reserved := map[string][32]byte{}
	for _, root := range plan.sources {
		batch, err := planLogs(root, paths.Directory, reserved)
		if err != nil {
			return plan, err
		}
		plan.files = append(plan.files, batch...)
	}
	plan.logs = len(plan.files)
	companions, err := planRawCompanions(plan.files)
	if err != nil {
		return plan, err
	}
	plan.files = append(plan.files, companions...)
	if configTransfer != nil {
		plan.files = append(plan.files, *configTransfer)
	}
	return plan, nil
}

func planRawCompanions(logs []transfer) ([]transfer, error) {
	var companions []transfer
	for _, log := range logs {
		raw := log.source + ".raw"
		data, err := fingerprint(raw)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		destination, err := freeDestination(log.destination+".raw", data.hash, nil)
		if err != nil {
			return nil, err
		}
		companions = append(companions, transfer{source: raw, destination: destination, sourceHash: data.hash, targetHash: data.hash, modified: data.modified})
	}
	return companions, nil
}

func planLogs(source, app string, reserved map[string][32]byte) ([]transfer, error) {
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("legacy log root is not a real directory: %s", source)
	}
	entries, err := logstore.ListLogFiles(source)
	if err != nil {
		return nil, err
	}
	var plans []transfer
	for _, entry := range entries {
		path := entry.Path
		summary, err := logstore.ReadSummary(path)
		if err != nil {
			return nil, fmt.Errorf("cannot validate legacy log %s: %w", entry.RelativePath, err)
		}
		subdir := "unclassified"
		args := strings.Fields(summary.Command)
		if len(args) > 0 {
			target := summary.Target
			if target == "" {
				target = logger.TargetAddress(args)
			}
			subdir = logger.LogSubdirFor(args[0], target)
		}
		dir, err := logstore.EnsureSubdir(app, filepath.Join("logs", subdir))
		if err != nil {
			return nil, err
		}
		data, err := fingerprint(path)
		if err != nil {
			return nil, err
		}
		target, err := freeDestination(filepath.Join(dir, filepath.Base(entry.RelativePath)), data.hash, reserved)
		if err != nil {
			return nil, err
		}
		reserved[target] = data.hash
		plans = append(plans, transfer{source: path, destination: target, sourceHash: data.hash, targetHash: data.hash, modified: data.modified})
	}
	return plans, nil
}

func freeDestination(path string, want [32]byte, reserved map[string][32]byte) (string, error) {
	ext := ".log"
	if strings.HasSuffix(path, ".log.gz") {
		ext = ".log.gz"
	} else if !strings.HasSuffix(path, ext) {
		ext = filepath.Ext(path)
	}
	base := strings.TrimSuffix(path, ext)
	for i := 0; ; i++ {
		candidate := path
		if i > 0 {
			candidate = fmt.Sprintf("%s_migrated_%d%s", base, i, ext)
		}
		if _, ok := reserved[candidate]; ok {
			continue
		}
		data, err := fingerprint(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		if data.hash == want {
			return candidate, nil
		}
	}
}

// Package migration copies legacy storage, verifies every destination, then
// removes only source files whose content is still identical to the snapshot.
package migration

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logger"
	"github.com/ryu/kuroko/internal/logstore"
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
	legacy := filepath.Join(home, ".config", "kuroko")
	app := filepath.Join(home, ".kuroko")
	root := filepath.Join(app, "logs")
	r := Result{Source: filepath.Join(legacy, "logs"), Destination: root}
	oldConfig := filepath.Join(legacy, "config.json")
	var configured string
	var settings map[string]json.RawMessage
	configBytes, err := readRegular(oldConfig)
	if err != nil && !os.IsNotExist(err) {
		return r, err
	}
	if err == nil {
		if err := json.Unmarshal(configBytes, &config.Config{}); err != nil {
			return r, fmt.Errorf("invalid legacy config: %w", err)
		}
		if err := json.Unmarshal(configBytes, &settings); err != nil {
			return r, fmt.Errorf("invalid legacy config: %w", err)
		}
		if settings == nil {
			return r, fmt.Errorf("legacy config must be a JSON object")
		}
		if value, ok := settings["log_dir"]; ok {
			if err := json.Unmarshal(value, &configured); err != nil {
				return r, fmt.Errorf("invalid legacy log_dir: %w", err)
			}
		}
		if configured != "" {
			r.Source = configured
		}
	}
	if sourceOverride != "" {
		if configured != "" && filepath.Clean(configured) != filepath.Clean(sourceOverride) {
			return r, fmt.Errorf("--from differs from legacy config log_dir; preserved the existing configuration")
		}
		r.Source = sourceOverride
	}
	if !filepath.IsAbs(r.Source) {
		return r, fmt.Errorf("migration requires an absolute source directory: %s", r.Source)
	}
	r.Source = filepath.Clean(r.Source)
	if overlaps(r.Source, root) || r.Source == filepath.Clean(home) || r.Source == string(filepath.Separator) {
		return r, fmt.Errorf("source and destination must be separate log directories")
	}
	if _, err := logstore.EnsureSubdir(app, "logs"); err != nil {
		return r, err
	}
	// Parent-directory symlinks can make lexically different roots refer to
	// the same files. Never treat that as a migration and remove the originals.
	if resolved, err := filepath.EvalSymlinks(r.Source); err == nil {
		destination, err := filepath.EvalSymlinks(root)
		if err != nil {
			return r, err
		}
		if overlaps(resolved, destination) {
			return r, fmt.Errorf("source resolves inside the destination storage; nothing was removed")
		}
	} else if !os.IsNotExist(err) {
		return r, err
	}
	lock, err := os.OpenFile(filepath.Join(app, ".migration.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return r, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return r, fmt.Errorf("another migration is running: %w", err)
	}

	// Validate configuration conflicts before copying or removing any log.
	var configTransfer *transfer
	if settings != nil {
		settings["log_dir"], _ = json.Marshal(root)
		translated, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return r, err
		}
		translated = append(translated, '\n')
		target := filepath.Join(app, "config.json")
		if existing, err := readRegular(target); err == nil {
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(existing, &parsed); err != nil || !sameJSON(parsed, settings) {
				return r, fmt.Errorf("new config conflicts with legacy config; both files have been preserved")
			}
			translated = existing // retain an equivalent user's formatting
		} else if !os.IsNotExist(err) {
			return r, err
		}
		info, err := os.Stat(oldConfig)
		if err != nil {
			return r, err
		}
		configTransfer = &transfer{source: oldConfig, destination: target, sourceHash: sha256.Sum256(configBytes), targetHash: sha256.Sum256(translated), modified: info.ModTime(), data: translated}
	} else if existing, err := readRegular(filepath.Join(app, "config.json")); err == nil {
		var cfg struct {
			LogDir string `json:"log_dir"`
		}
		if err := json.Unmarshal(existing, &cfg); err != nil {
			return r, err
		}
		if cfg.LogDir != "" && filepath.Clean(cfg.LogDir) != root {
			return r, fmt.Errorf("new config uses a different log directory; preserved existing configuration")
		}
	} else if !os.IsNotExist(err) {
		return r, err
	}

	sources := []string{r.Source}
	legacyLogs := filepath.Join(legacy, "logs")
	if legacyLogs != r.Source {
		if _, err := os.Lstat(legacyLogs); err == nil {
			sources = append(sources, legacyLogs)
		} else if !os.IsNotExist(err) {
			return r, err
		}
	}
	var plans []transfer
	reserved := map[string][32]byte{}
	for _, source := range sources {
		batch, err := planLogs(source, app, reserved)
		if err != nil {
			return r, err
		}
		plans = append(plans, batch...)
	}
	r.Logs = len(plans)
	// Include raw debug companions without dropping their original bytes.
	for _, p := range append([]transfer(nil), plans...) {
		raw := p.source + ".raw"
		if data, err := fingerprint(raw); err == nil {
			dst, err := freeDestination(p.destination+".raw", data.hash, nil)
			if err != nil {
				return r, err
			}
			plans = append(plans, transfer{source: raw, destination: dst, sourceHash: data.hash, targetHash: data.hash, modified: data.modified})
		} else if !os.IsNotExist(err) {
			return r, err
		}
	}
	if configTransfer != nil {
		plans = append(plans, *configTransfer)
	}
	for _, p := range plans {
		if err := copyVerified(p); err != nil {
			return r, fmt.Errorf("copy verification failed; sources preserved: %w", err)
		}
	}
	// Full verification barrier: no source is removed until every copy and
	// every source snapshot has been checked, including configuration.
	for _, p := range plans {
		if err := verify(p); err != nil {
			return r, fmt.Errorf("migration verification failed; sources preserved: %w", err)
		}
	}
	for _, p := range plans {
		if err := removeVerified(p); err != nil {
			return r, err
		}
		r.Files++
		if p.source == oldConfig {
			r.ConfigMigrated = true
		} else {
			if info, err := os.Stat(p.destination); err == nil {
				r.Bytes += info.Size()
			}
		}
	}
	for _, source := range sources {
		if err := removeEmptyTree(source); err != nil {
			return r, err
		}
		if _, err := os.Lstat(source); err == nil {
			r.SourceRemaining = true
		} else if !os.IsNotExist(err) {
			return r, err
		}
	}
	// Only remove the known legacy application directory when it is empty.
	if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
		return r, err
	}
	return r, nil
}

func sameJSON(a, b map[string]json.RawMessage) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	var x, y any
	if json.Unmarshal(aa, &x) != nil || json.Unmarshal(bb, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func overlaps(a, b string) bool {
	within := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
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
		path := filepath.Join(source, entry.Name())
		summary, err := logstore.ReadSummary(path)
		if err != nil {
			return nil, fmt.Errorf("cannot validate legacy log %s: %w", entry.Name(), err)
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
		target, err := freeDestination(filepath.Join(dir, filepath.Base(entry.Name())), data.hash, reserved)
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

func removeEmptyTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if e.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
			return err
		}
	}
	return nil
}

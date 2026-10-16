package migration

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaterFailureNeverRemovesEarlierSources(t *testing.T) {
	for _, stage := range []string{"copy", "verify"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			var transfers []transfer
			for _, name := range []string{"first", "second"} {
				source := filepath.Join(dir, "old", name+".log")
				put(t, source, []byte(name+" original"))
				data, err := fingerprint(source)
				if err != nil {
					t.Fatal(err)
				}
				transfers = append(transfers, transfer{source: source, destination: filepath.Join(dir, name+".log"), sourceHash: data.hash, targetHash: data.hash, modified: data.modified})
			}
			// A changed second source fails during copying unless a valid
			// destination already exists, in which case verification catches it.
			put(t, transfers[1].source, []byte("second changed"))
			if stage == "verify" {
				put(t, transfers[1].destination, []byte("second original"))
			}
			plan := migrationPlan{files: transfers}
			var result Result
			err := plan.execute(&result)
			if err == nil {
				t.Fatal("accepted a changed source")
			}
			if result.Files != 0 {
				t.Fatalf("removed %d sources before all files passed %s", result.Files, stage)
			}
			expectBytes(t, transfers[0].source, []byte("first original"))
			expectBytes(t, transfers[1].source, []byte("second changed"))
			expectBytes(t, transfers[0].destination, []byte("first original"))
		})
	}
}

func TestMigrationPreservesConflictingLargeIntegerSettings(t *testing.T) {
	home := t.TempDir()
	legacyConfig := filepath.Join(home, ".config", "kuroko", "config.json")
	newConfig := filepath.Join(home, ".kuroko", "config.json")
	oldData := []byte(`{"future_id":9007199254740993}`)
	root, err := json.Marshal(filepath.Join(home, ".kuroko", "logs"))
	if err != nil {
		t.Fatal(err)
	}
	newData := []byte(`{"future_id":9007199254740992,"log_dir":` + string(root) + `}`)
	put(t, legacyConfig, oldData)
	put(t, newConfig, newData)
	_, err = Run(home, "")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected a config conflict: %v", err)
	}
	expectBytes(t, legacyConfig, oldData)
	expectBytes(t, newConfig, newData)
}

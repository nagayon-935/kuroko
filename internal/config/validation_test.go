package config

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestInspectValidation(t *testing.T) {
	t.Setenv("KUROKO_WEBHOOK_URL", "")
	t.Setenv("KUROKO_NOTIFIER_INCLUDE_ARGS", "false")
	t.Setenv("KUROKO_NOTIFIER_INCLUDE_LOG", "false")
	for _, tt := range []struct {
		name, data, notifierEnv string
		paths                   []string
		suggestion              string
		hardError               bool
	}{
		{name: "unknown top-level key", data: `{"extra":1e999}`, paths: []string{"extra"}},
		{name: "nested typo", data: `{"redaction":{"enable":true}}`, paths: []string{"redaction.enable"}, suggestion: `did you mean "enabled"?`},
		{name: "rule key", data: `{"banner":{"rules":[{"match":"a","color":"red"},{"match":"b","color":"cyan","colour":"red"}]}}`, paths: []string{"banner.rules[1].colour"}, suggestion: `did you mean "color"?`},
		{name: "unknown notifier", data: `{"notifier":{"type":"slak"}}`, paths: []string{"notifier.type"}},
		{name: "unknown notifier override", data: `{"notifier":{"type":"slack"}}`, notifierEnv: "slak", paths: []string{"KUROKO_NOTIFIER"}},
		{name: "valid notifier override", data: `{"notifier":{"type":"slak"}}`, notifierEnv: "none"},
		{name: "empty notifier", data: `{"notifier":{"type":""}}`},
		{name: "discord missing webhook", data: `{"notifier":{"type":"discord"}}`, paths: []string{"notifier.webhook_url"}},
		{name: "slack missing webhook", data: "{}", notifierEnv: "slack", paths: []string{"notifier.webhook_url"}},
		{name: "negative storage", data: `{"storage":{"compress_threshold_mb":-1,"rotation":{"max_age_days":-2,"max_total_size_mb":-3}}}`, paths: []string{"storage.compress_threshold_mb", "storage.rotation.max_age_days", "storage.rotation.max_total_size_mb"}},
		{name: "invalid banner", data: `{"banner":{"rules":[{"match":"","color":"pink"}]}}`, paths: []string{"banner.rules[0].match", "banner.rules[0].color"}},
		{name: "clean config", data: `{"redaction":{"enabled":true},"notifier":{"type":"slack","webhook_url":"https://example.test","include_log":false,"include_args":true},"banner":{"rules":[{"match":"prod","label":"Production","color":"yellow"}]}}`},
		{name: "invalid JSON", data: `{"storage":`, hardError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("KUROKO_NOTIFIER", tt.notifierEnv)
			paths := PathsForHome(home)
			if err := os.MkdirAll(paths.Directory, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.ConfigFile, []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}

			report, err := Inspect(Options{})

			if tt.hardError {
				if err == nil {
					t.Fatal("expected a parse error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, issue := range report.Issues {
				got = append(got, issue.Path)
				if issue.Severity != "warning" || issue.Message == "" || !strings.Contains(issue.Message, tt.suggestion) {
					t.Errorf("unexpected issue: %+v", issue)
				}
			}
			if !slices.Equal(got, tt.paths) {
				t.Errorf("paths = %v, want %v", got, tt.paths)
			}
		})
	}
}

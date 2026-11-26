package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nagayon-935/kuroko/internal/config"
)

func TestConfigCheck(t *testing.T) {
	const webhook = "https://example.test/secret-webhook"
	for _, tt := range []struct {
		name, data string
		legacy     bool
		wantCode   int
		want       []string
	}{
		{name: "no file", want: []string{"Config file: (none)", "banner.enabled: true", "banner.rules: 0", "notifier.type: none", "webhook configured: no"}},
		{name: "effective settings", data: `{"log_dir":"from-file","redaction":{"enabled":true},"banner":{"enabled":false,"rules":[{"match":"prod","label":"PROD","color":"red"}]},"notifier":{"type":"slack","webhook_url":"` + webhook + `","include_log":false},"storage":{"compress_on_close":true,"compress_threshold_mb":7,"rotation":{"enabled":true,"max_age_days":12,"max_total_size_mb":99}}}`,
			want: []string{"redaction.enabled: true", "banner.enabled: false", "banner.rules: 1", "notifier.type: slack", "webhook configured: yes", "notifier.include_log: true", "notifier.include_args: false", "storage.rotation.enabled: true", "storage.rotation.max_age_days: 12", "storage.rotation.max_total_size_mb: 99", "storage.compress_on_close: true", "storage.compress_threshold_mb: 7"}},
		{name: "legacy with issues", legacy: true, data: `{"redaction":{"enable":true},"notifier":{"type":"slak","webhook_url":"` + webhook + `"}}`, wantCode: 1, want: []string{`warning: redaction.enable: unknown key; did you mean "enabled"?`, "warning: notifier.type:", "webhook configured: yes", "redaction.enabled: false"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logDir := isolate(t)
			t.Setenv("KUROKO_LOG_DIR", "from-env")
			t.Setenv("KUROKO_NOTIFIER_INCLUDE_LOG", "true")
			t.Setenv("KUROKO_NOTIFIER_INCLUDE_ARGS", "false")
			paths := config.PathsForHome(os.Getenv("HOME"))
			file := paths.ConfigFile
			if tt.legacy {
				file = paths.LegacyConfigFile
			}
			if tt.data != "" {
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(tt.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			code, out, stderr := runCLI(t, "--log-dir", logDir, "config", "check")

			if code != tt.wantCode || stderr != "" || strings.Contains(out, webhook) {
				t.Fatalf("exit = %d, stderr = %q, webhook leaked = %t", code, stderr, strings.Contains(out, webhook))
			}
			want := append(tt.want, "log_dir: "+logDir)
			if tt.data != "" {
				want = append(want, "Config file: "+file)
			}
			for _, line := range want {
				if !strings.Contains(out, line) {
					t.Errorf("missing %q in %s", line, out)
				}
			}
		})
	}
}

func TestConfigWarningDispatch(t *testing.T) {
	for _, tt := range []struct {
		name, notifier string
		args           []string
		wantWarning    bool
	}{
		{"normal warns and continues", "slak", []string{"logs", "--info"}, true},
		{"clean is silent", "none", []string{"logs", "--info"}, false},
		{"completion is silent", "slak", []string{"__complete", "config"}, false},
		{"check uses stdout", "slak", []string{"config", "check"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logDir := isolate(t)
			t.Setenv("KUROKO_NOTIFIER", tt.notifier)
			capture, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stderr
			os.Stderr = capture
			t.Cleanup(func() { os.Stderr = original; capture.Close() })

			code, out, stderr := runCLI(t, append([]string{"-d", logDir}, tt.args...)...)

			data, err := os.ReadFile(capture.Name())
			if err != nil {
				t.Fatal(err)
			}
			wantCode := 0
			if tt.args[0] == "config" {
				wantCode = 1
			}
			if code != wantCode || stderr != "" {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			if tt.wantWarning {
				if !strings.Contains(string(data), "\x1b[33m[kuroko] warning: KUROKO_NOTIFIER:") {
					t.Errorf("stderr = %q", data)
				}
			} else if len(data) != 0 {
				t.Errorf("unexpected stderr = %q", data)
			}
			if tt.args[0] == "__complete" && out != "check\n" {
				t.Errorf("completion = %q", out)
			}
		})
	}
}

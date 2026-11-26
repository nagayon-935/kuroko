package main

import (
	"fmt"
	"io"

	"github.com/nagayon-935/kuroko/internal/config"
)

func runConfig(args []string, logDir string, stdout, stderr io.Writer) int {
	if isHelpFlag(args) {
		fmt.Fprint(stderr, configUsage)
		return 0
	}
	if len(args) != 1 || args[0] != "check" {
		return fail(stderr, "usage: kuroko config check")
	}
	return runConfigCheck(logDir, stdout, stderr)
}

func runConfigCheck(logDir string, stdout, stderr io.Writer) int {
	report, err := config.Inspect(config.Options{LogDir: logDir})
	if err != nil {
		return fail(stderr, "config error: %v", err)
	}
	file := report.File
	if file == "" {
		file = "(none)"
	}
	fmt.Fprintln(stdout, "Config file:", file)
	for _, issue := range report.Issues {
		fmt.Fprintln(stdout, issue)
	}
	webhook := "no"
	if report.Notifier.WebhookURL != "" {
		webhook = "yes"
	}
	fmt.Fprintf(stdout, `log_dir: %s
redaction.enabled: %t
banner.enabled: %t
banner.rules: %d
notifier.type: %s
webhook configured: %s
notifier.include_log: %t
notifier.include_args: %t
storage.rotation.enabled: %t
storage.rotation.max_age_days: %d
storage.rotation.max_total_size_mb: %d
storage.compress_on_close: %t
storage.compress_threshold_mb: %d
`, report.LogDir, report.Redaction.Enabled, report.Banner.Enabled, len(report.Banner.Rules),
		report.Notifier.Type, webhook,
		report.Notifier.IncludeLog, report.Notifier.IncludeArgs,
		report.Storage.Rotation.Enabled, report.Storage.Rotation.MaxAgeDays, report.Storage.Rotation.MaxTotalSizeMB,
		report.Storage.CompressOnClose, report.Storage.CompressThresholdMB)
	if len(report.Issues) > 0 {
		return 1
	}
	return 0
}

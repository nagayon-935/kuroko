package main

import (
	"fmt"
	"io"

	"github.com/nagayon-935/kuroko/internal/config"
)

func runConfigCheck(logDir string, stdout io.Writer) int {
	report, err := config.Inspect(config.Options{LogDir: logDir})
	if err != nil {
		return fail("config error: %v", err)
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
notifier.type: %s
webhook configured: %s
notifier.include_log: %t
notifier.include_args: %t
storage.rotation.enabled: %t
storage.rotation.max_age_days: %d
storage.rotation.max_total_size_mb: %d
storage.compress_on_close: %t
storage.compress_threshold_mb: %d
`, report.LogDir, report.Redaction.Enabled, report.Notifier.Type, webhook,
		report.Notifier.IncludeLog, report.Notifier.IncludeArgs,
		report.Storage.Rotation.Enabled, report.Storage.Rotation.MaxAgeDays, report.Storage.Rotation.MaxTotalSizeMB,
		report.Storage.CompressOnClose, report.Storage.CompressThresholdMB)
	if len(report.Issues) > 0 {
		return 1
	}
	return 0
}

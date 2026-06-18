package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/session"
	"github.com/ryu/kuroko/internal/viewer"
)

// version is overridden at build time via -ldflags "-X main.version=v1.2.3"
var version = "dev"

const usage = `kuroko — transparent terminal session logger

Usage:
  kuroko [options] <command> [args...]

Options:
  -d, --log-dir <dir>   Save logs to <dir> (overrides config and env var)
  -h, --help            Show this help
  -v, --version         Show version

Examples:
  kuroko ssh user@hostname
  kuroko -d /tmp/logs ssh user@hostname
  kuroko --log-dir ~/work/logs screen /dev/ttyUSB0 115200
  kuroko bash
  kuroko logs           List saved logs
  kuroko view <logfile>  View a saved log session interactively

Logs are saved to ~/.config/kuroko/logs/ by default.

Priority: --log-dir flag > $KUROKO_LOG_DIR > config.json > default

Environment variables:
  KUROKO_LOG_DIR      Override log directory
  KUROKO_NOTIFIER     Notifier type: none | discord | slack  (default: none)
  KUROKO_WEBHOOK_URL  Webhook URL for discord/slack notifier
`

func main() {
	fs := flag.NewFlagSet("kuroko", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	var logDir string
	fs.StringVar(&logDir, "log-dir", "", "")
	fs.StringVar(&logDir, "d", "", "")

	var showHelp, showVersion bool
	fs.BoolVar(&showHelp, "help", false, "")
	fs.BoolVar(&showHelp, "h", false, "")
	fs.BoolVar(&showVersion, "version", false, "")
	fs.BoolVar(&showVersion, "v", false, "")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}

	if showHelp {
		fmt.Print(usage)
		os.Exit(0)
	}
	if showVersion {
		fmt.Println("kuroko " + version)
		os.Exit(0)
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	// Built-in sub-commands (after flag parsing)
	switch rest[0] {
	case "help":
		fmt.Print(usage)
		os.Exit(0)
	case "logs":
		if len(rest) == 1 {
			cfg, err := config.Load(config.Options{LogDir: logDir})
			if err != nil {
				fatalf("config error: %v", err)
			}
			if err := viewer.RunSelector(cfg); err != nil {
				fatalf("selector error: %v", err)
			}
		} else {
			showLogs(config.Options{LogDir: logDir})
		}
		os.Exit(0)
	case "view":
		if len(rest) < 2 {
			fatalf("usage: kuroko view <logfile>")
		}
		if err := viewer.Run(rest[1]); err != nil {
			fatalf("viewer error: %v", err)
		}
		os.Exit(0)
	}

	cfg, err := config.Load(config.Options{LogDir: logDir})
	if err != nil {
		fatalf("config error: %v", err)
	}

	sess, err := session.New(cfg, rest)
	if err != nil {
		fatalf("session error: %v", err)
	}

	exitCode, err := sess.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m[kuroko] error: %v\033[0m\n", err)
	}
	os.Exit(exitCode)
}

func showLogs(opt config.Options) {
	cfg, err := config.Load(opt)
	if err != nil {
		fatalf("config error: %v", err)
	}

	entries, err := os.ReadDir(cfg.LogDir)
	if err != nil {
		fatalf("reading log dir: %v", err)
	}
	if len(entries) == 0 {
		fmt.Printf("No logs in %s\n", cfg.LogDir)
		return
	}

	fmt.Printf("Logs in %s:\n\n", cfg.LogDir)
	for _, e := range entries {
		info, _ := e.Info()
		fmt.Printf("  %-50s  %s\n", e.Name(), info.ModTime().Format("2006-01-02 15:04:05"))
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\033[31m[kuroko] "+format+"\033[0m\n", args...)
	os.Exit(1)
}

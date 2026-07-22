package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ryu/kuroko/internal/completion"
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
  kuroko completion bash  Print a bash completion script

Logs are saved to ~/.config/kuroko/logs/ by default.

Shell completion (bash):
  source <(kuroko completion bash)                                    # current shell only
  kuroko completion bash > ~/.local/share/bash-completion/completions/kuroko  # permanent

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
		cfg, err := config.Load(config.Options{LogDir: logDir})
		if err != nil {
			fatalf("config error: %v", err)
		}
		if err := viewer.RunSelector(cfg); err != nil {
			fatalf("selector error: %v", err)
		}
		os.Exit(0)
	case "view":
		if len(rest) < 2 {
			fatalf("usage: kuroko view <logfile>")
		}
		if err := viewer.Run(filepath.Clean(rest[1])); err != nil {
			fatalf("viewer error: %v", err)
		}
		os.Exit(0)
	case "completion":
		shell := "bash"
		if len(rest) >= 2 {
			shell = rest[1]
		}
		if err := completion.WriteScript(os.Stdout, shell); err != nil {
			fatalf("completion error: %v", err)
		}
		os.Exit(0)
	case "__complete":
		// Hidden helper invoked by the shell completion script; not
		// listed in usage. rest[1:] is the already-typed context
		// (kuroko itself and the word under the cursor excluded).
		cfg, err := config.Load(config.Options{LogDir: logDir})
		if err != nil {
			os.Exit(0) // completion must never surface errors to the shell
		}
		for _, c := range completion.Candidates(rest[1:], cfg) {
			fmt.Println(c)
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

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\033[31m[kuroko] "+format+"\033[0m\n", args...)
	os.Exit(1)
}

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/nagayon-935/kuroko/internal/completion"
	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/console"
	"github.com/nagayon-935/kuroko/internal/launcher"
	"github.com/nagayon-935/kuroko/internal/session"
	"github.com/nagayon-935/kuroko/internal/viewer"
)

// version is overridden at build time via -ldflags "-X main.version=v1.2.3"
var version = "dev"

func init() {
	// `go install github.com/nagayon-935/kuroko/cmd/kuroko@v1.2.3` sets no
	// ldflags, but records the module version in the binary.
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
}

const usage = `kuroko — transparent terminal session logger

Usage:
  kuroko [options]                    Choose an SSH or serial connection
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
  kuroko logs --info    Show storage root, counts and disk usage
  kuroko migrate        Verify and migrate legacy logs and config to ~/.kuroko
  kuroko view <logfile>  View a saved log session interactively
  kuroko view --last     View the most recent session
  kuroko export --last --command 1 -o command.txt  Export a command and output
  kuroko export <logfile>  Export an entire session to stdout
  kuroko completion bash  Print a bash completion script

Logs are saved to ~/.kuroko/logs/ by default.

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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes kuroko with the command-line arguments args (without the
// program name) and returns the process exit code. It is split from main so
// the dispatch can be tested without exiting the process.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("kuroko", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }

	var logDir string
	fs.StringVar(&logDir, "log-dir", "", "")
	fs.StringVar(&logDir, "d", "", "")

	var showHelp, showVersion bool
	fs.BoolVar(&showHelp, "help", false, "")
	fs.BoolVar(&showHelp, "h", false, "")
	fs.BoolVar(&showVersion, "version", false, "")
	fs.BoolVar(&showVersion, "v", false, "")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	if showHelp {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if showVersion {
		fmt.Fprintln(stdout, "kuroko "+version)
		return 0
	}

	rest := fs.Args()
	if len(rest) == 0 {
		var err error
		rest, err = launcher.Run()
		if err != nil {
			return fail("connection selection: %v", err)
		}
		if len(rest) == 0 {
			return 0 // selection cancelled
		}
	}

	// Built-in sub-commands (after flag parsing)
	switch rest[0] {
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "logs":
		if err := runLogs(rest[1:], logDir, stdout, stderr); err != nil {
			return fail("logs error: %v", err)
		}
		return 0
	case "migrate":
		if err := runMigration(rest[1:], stdout, stderr); err != nil {
			return fail("migration error: %v", err)
		}
		return 0
	case "view":
		path, err := resolveLogArgument(rest[1:], logDir)
		if err != nil {
			return fail("view error: %v", err)
		}
		if err := viewer.Run(path); err != nil {
			return fail("viewer error: %v", err)
		}
		return 0
	case "export":
		if err := runExport(rest[1:], logDir, stdout, stderr); err != nil {
			return fail("export error: %v", err)
		}
		return 0
	case "completion":
		shell := "bash"
		if len(rest) >= 2 {
			shell = rest[1]
		}
		if err := completion.WriteScript(stdout, shell); err != nil {
			return fail("completion error: %v", err)
		}
		return 0
	case "__complete":
		// Hidden helper invoked by the shell completion script; not
		// listed in usage. rest[1:] is the already-typed context
		// (kuroko itself and the word under the cursor excluded).
		cfg, err := config.Resolve(config.Options{LogDir: logDir})
		if err != nil {
			return 0 // completion must never surface errors to the shell
		}
		for _, c := range completion.Candidates(rest[1:], cfg) {
			fmt.Fprintln(stdout, c)
		}
		return 0
	}

	cfg, err := config.Load(config.Options{LogDir: logDir})
	if err != nil {
		return fail("config error: %v", err)
	}

	sess, err := session.New(cfg, rest)
	if err != nil {
		return fail("session error: %v", err)
	}

	exitCode, err := sess.Run()
	if err != nil {
		console.Errorf("error: %v", err)
	}
	return exitCode
}

// fail reports an error and returns the exit code for it.
func fail(format string, args ...any) int {
	console.Errorf(format, args...)
	return 1
}

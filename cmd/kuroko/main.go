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
  kuroko view --follow --last  Follow the most recent session as it runs (-f)
  kuroko grep -i 'error|down'  Search all saved logs (-l: names only)
  kuroko grep --open -i 'error'  Open the match in the viewer (pick one if several)
  kuroko view --last --line 120  Open at raw line 120 (a line number from grep)
  kuroko export --last --command 1 -o command.txt  Export a command and output
  kuroko export <logfile>  Export an entire session to stdout
  kuroko config check   Show effective settings and report configuration issues
  kuroko completion <bash|zsh|fish>  Print a shell completion script

kuroko view --follow streams the log like tail -f and exits when the session
ends or on Ctrl+C. Output trails the live session by up to about one screen
height, because the logger holds back recent rows that cursor-up redraws may
still rewrite.

kuroko grep prints <path>:<line>:<text> per match (Go/RE2 syntax). It exits 0 if
anything matched, 1 if nothing matched, and 2 on any error (including logs that
could not be read).

kuroko grep --open collects the matches and opens the command whose output
contains the line: the viewer directly for one match, a list to choose from
for several (first 10000). Enter opens, q/Esc quits. It needs a terminal, and
cannot be combined with -l. kuroko view [--last | <logfile>] --line N does the
same for a line number as grep prints it (N counts every line of the raw log);
it cannot be combined with --follow.

Logs are saved to ~/.kuroko/logs/ by default.

Shell completion (bash):
  eval "$(kuroko completion bash)"                                    # current shell only
  kuroko completion bash > ~/.local/share/bash-completion/completions/kuroko  # permanent

Priority: --log-dir flag > $KUROKO_LOG_DIR > config.json > default

Environment variables:
  KUROKO_LOG_DIR                Override log directory
  KUROKO_NOTIFIER               Notifier type: none | discord | slack  (default: none)
  KUROKO_WEBHOOK_URL            Webhook URL for discord/slack notifier
  KUROKO_NOTIFIER_INCLUDE_ARGS  Include command arguments in notifications (true/false)
  KUROKO_NOTIFIER_INCLUDE_LOG   Attach/quote session log in end notifications (true/false)
`

const (
	logsUsage       = "usage: kuroko logs [--info]\nWithout --info, opens the interactive log selector.\n  --info  show storage root, counts, sizes and groups\n"
	migrateUsage    = "usage: kuroko migrate [--from /absolute/log/directory]\nCopies legacy logs and config to ~/.kuroko, verifies SHA-256 checksums, then removes verified sources.\n"
	configUsage     = "usage: kuroko config check\nShows effective settings and reports configuration issues.\n"
	completionUsage = "usage: kuroko completion [bash|zsh|fish]\nPrints a shell completion script (defaults to bash).\n"
)

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
			return fail(stderr, "connection selection: %v", err)
		}
		if len(rest) == 0 {
			return 0 // selection cancelled
		}
	}

	// Built-in sub-commands (after flag parsing)
	switch rest[0] {
	case "help":
		return runHelp(rest[1:], stdout, stderr)
	case "logs":
		if err := runLogs(rest[1:], logDir, stdout, stderr); err != nil {
			return fail(stderr, "logs error: %v", err)
		}
		return 0
	case "migrate":
		if err := runMigration(rest[1:], stdout, stderr); err != nil {
			return fail(stderr, "migration error: %v", err)
		}
		return 0
	case "view":
		if inViewer, err := runView(rest[1:], logDir, stdout, stderr); err != nil {
			if inViewer {
				return fail(stderr, "viewer error: %v", err)
			}
			return fail(stderr, "view error: %v", err)
		}
		return 0
	case "grep":
		code, err := runGrep(rest[1:], logDir, stdout, stderr)
		if err != nil {
			fail(stderr, "grep error: %v", err)
		}
		return code
	case "export":
		if err := runExport(rest[1:], logDir, stdout, stderr); err != nil {
			return fail(stderr, "export error: %v", err)
		}
		return 0
	case "config":
		return runConfig(rest[1:], logDir, stdout, stderr)
	case "completion":
		if err := runCompletion(rest[1:], stdout, stderr); err != nil {
			return fail(stderr, "completion error: %v", err)
		}
		return 0
	case "__complete":
		// Hidden helper invoked by the shell completion script; not
		// listed in usage. rest[1:] is the already-typed context
		// (kuroko itself and the word under the cursor excluded).
		report, err := config.Inspect(config.Options{LogDir: logDir})
		if err != nil {
			return 0 // completion must never surface errors to the shell
		}
		home, _ := os.UserHomeDir() // "" just skips ~/.ssh/config hosts
		_ = completion.Write(stdout, rest[1:], report.Config, home)
		return 0
	}

	cfg, err := config.Load(config.Options{LogDir: logDir})
	if err != nil {
		return fail(stderr, "config error: %v", err)
	}

	sess, err := session.New(cfg, rest)
	if err != nil {
		return fail(stderr, "session error: %v", err)
	}

	exitCode, err := sess.Run()
	if err != nil {
		console.Ferrorf(stderr, "error: %v", err)
	}
	return exitCode
}

func runHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if len(args) > 1 {
		return fail(stderr, "usage: kuroko help [subcommand]")
	}
	switch args[0] {
	case "logs":
		fmt.Fprint(stdout, logsUsage)
	case "view":
		fmt.Fprint(stdout, viewUsage)
	case "grep":
		fmt.Fprint(stdout, grepUsage)
	case "export":
		fmt.Fprint(stdout, exportUsage)
	case "migrate":
		fmt.Fprint(stdout, migrateUsage)
	case "config":
		fmt.Fprint(stdout, configUsage)
	case "completion":
		fmt.Fprint(stdout, completionUsage)
	default:
		return fail(stderr, "unknown help topic %q", args[0])
	}
	return 0
}

func runCompletion(args []string, stdout, stderr io.Writer) error {
	if isHelpFlag(args) {
		fmt.Fprint(stderr, completionUsage)
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: kuroko completion [bash|zsh|fish]")
	}
	shell := "bash"
	if len(args) == 1 {
		shell = args[0]
	}
	return completion.WriteScript(stdout, shell)
}

func isHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

// fail reports an error on stderr and returns the exit code for it.
func fail(stderr io.Writer, format string, args ...any) int {
	console.Ferrorf(stderr, format, args...)
	return 1
}

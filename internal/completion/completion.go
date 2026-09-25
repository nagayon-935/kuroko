// Package completion computes shell tab-completion candidates for the
// kuroko CLI and renders the per-shell registration script that wires a
// shell's completion engine up to `kuroko __complete`.
package completion

import (
	"strconv"
	"strings"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logstore"
)

// subcommands are the built-in kuroko subcommand words (in addition to
// wrapping an arbitrary PATH command).
var subcommands = []string{"logs", "view", "help", "completion"}

// globalFlags are accepted before the subcommand/wrapped-command word.
// Go's flag package stops parsing at the first non-flag word, so they are
// never offered after it.
var globalFlags = []string{"--log-dir", "-d", "--help", "-h", "--version", "-v"}

// logDirFlagNames are the names (leading dashes stripped) that take the
// log-directory value; flag accepts both -name and --name for each.
var logDirFlagNames = map[string]bool{"log-dir": true, "d": true}

// exitFlagNames are boolean flags that make main print and exit before any
// subcommand runs, so nothing after them is worth completing.
var exitFlagNames = map[string]bool{"help": true, "h": true, "version": true, "v": true}

// shells are the shell names accepted by `kuroko completion <shell>`.
var shells = []string{"bash", "zsh", "fish"}

// Candidates returns the completion candidates for the word currently being
// typed, given ctx: the previously-typed words (kuroko itself and the word
// under the cursor already excluded). cfg supplies the default log
// directory, which ctx may override via -d/--log-dir.
func Candidates(ctx []string, cfg *config.Config) []string {
	p := parseContext(ctx)
	if p.exits {
		return nil
	}

	logDir := cfg.LogDir
	if p.logDirOverride != "" {
		logDir = p.logDirOverride
	}

	switch p.sub {
	case "":
		return append(append([]string{}, subcommands...), globalFlags...)
	case "view":
		// view takes exactly one log file; main ignores anything after it.
		if len(p.args) > 0 {
			return nil
		}
		return logFileNames(logDir)
	case "completion":
		if len(p.args) > 0 {
			return nil
		}
		return append([]string{}, shells...)
	default:
		// logs/help take no arguments; any other word is a wrapped
		// command (e.g. ssh, bash) whose own arguments are out of scope.
		return nil
	}
}

// parsedContext is what main's flag parsing would make of ctx.
type parsedContext struct {
	sub            string   // first non-flag word; "" if none yet
	args           []string // words after sub
	logDirOverride string   // last -d/--log-dir value; "" = use config
	exits          bool     // a help/version flag makes main exit early
}

// parseContext mirrors how main's flag.FlagSet reads ctx: flags are only
// recognised before the first non-flag word (the subcommand), "--" ends
// flag parsing, and the last -d/--log-dir wins. An empty override is kept
// as "" so that, as in config.Load, the configured directory applies.
//
// The log-dir value may be given as "-d DIR", "--log-dir=DIR", or — since
// bash's default COMP_WORDBREAKS splits on '=' — as "--log-dir", "=", "DIR".
func parseContext(ctx []string) parsedContext {
	var p parsedContext

	for i := 0; i < len(ctx); i++ {
		tok := ctx[i]

		if tok == "--" {
			return withSub(p, ctx[i+1:])
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			return withSub(p, ctx[i:])
		}

		name, value, hasValue := strings.Cut(strings.TrimLeft(tok, "-"), "=")
		switch {
		case exitFlagNames[name]:
			// Only an explicit false value lets parsing continue; an
			// invalid value makes flag.Parse fail, which also exits.
			if b, err := strconv.ParseBool(value); !hasValue || err != nil || b {
				p.exits = true
				return p
			}
		case logDirFlagNames[name] && hasValue:
			p.logDirOverride = value
		case logDirFlagNames[name]:
			if i+1 < len(ctx) && ctx[i+1] == "=" {
				i++
			}
			if i+1 < len(ctx) {
				p.logDirOverride = ctx[i+1]
				i++
			}
		}
	}

	return p
}

// withSub records rest[0] as the subcommand and the remainder as its args.
func withSub(p parsedContext, rest []string) parsedContext {
	if len(rest) == 0 {
		return p
	}
	p.sub = rest[0]
	p.args = rest[1:]
	return p
}

func logFileNames(logDir string) []string {
	entries, err := logstore.ListLogFiles(logDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

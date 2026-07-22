// Package completion computes shell tab-completion candidates for the
// kuroko CLI and renders the per-shell registration script that wires a
// shell's completion engine up to `kuroko __complete`.
package completion

import (
	"strings"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logstore"
)

// subcommands are the built-in kuroko subcommand words (in addition to
// wrapping an arbitrary PATH command).
var subcommands = []string{"logs", "view", "help", "completion"}

// globalFlags are accepted before the subcommand/wrapped-command word.
var globalFlags = []string{"--log-dir", "-d", "--help", "-h", "--version", "-v"}

// shells are the shell names accepted by `kuroko completion <shell>`.
var shells = []string{"bash", "zsh", "fish"}

// Candidates returns the completion candidates for the word currently being
// typed, given ctx: the previously-typed words (kuroko itself and the word
// under the cursor already excluded). cfg supplies the default log
// directory, which ctx may override via -d/--log-dir.
func Candidates(ctx []string, cfg *config.Config) []string {
	sub, logDir := parseContext(ctx, cfg.LogDir)

	switch sub {
	case "":
		return append(append([]string{}, subcommands...), globalFlags...)
	case "view":
		return logFileNames(logDir)
	case "completion":
		return append([]string{}, shells...)
	case "logs", "help":
		return append([]string{}, globalFlags...)
	default:
		// Unrecognized leading word: treat it as a wrapped command
		// (e.g. ssh, bash) whose own arguments are out of scope here.
		return nil
	}
}

// parseContext walks ctx to find the selected subcommand (the first
// non-flag word) and any -d/--log-dir override. defaultLogDir is used when
// no override is present.
func parseContext(ctx []string, defaultLogDir string) (sub, logDir string) {
	logDir = defaultLogDir

	for i := 0; i < len(ctx); i++ {
		tok := ctx[i]

		if tok == "-d" || tok == "--log-dir" {
			if i+1 < len(ctx) {
				logDir = ctx[i+1]
				i++
			}
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if sub == "" {
			sub = tok
		}
	}

	return sub, logDir
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

package completion

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/sshconfig"
)

// wrappedDirective prefixes the first line `kuroko __complete` prints when
// the command line wraps another command. Its value is the index of that
// command's word in the full command line (kuroko itself is 0), which the
// shell script hands to bash-completion's _command_offset so the wrapped
// command's own completion (e.g. ssh's) takes over. Candidates after the
// directive are a fallback for shells without bash-completion.
const wrappedDirective = ":wrapped="

// WrappedIndex returns the index, in the full command line (kuroko itself
// is 0), of the command kuroko will wrap, or 0 if ctx names no wrapped
// command (a builtin subcommand, an early-exit flag, or nothing yet).
func WrappedIndex(ctx []string) int {
	p := parseContext(ctx)
	if p.exits || p.sub == "" || builtins[p.sub] {
		return 0
	}
	return p.subIndex + 1
}

// Write prints the completion response for ctx: an optional wrapped
// directive line, then one candidate per line.
func Write(w io.Writer, ctx []string, cfg *config.Config, home string) error {
	if i := WrappedIndex(ctx); i > 0 {
		if _, err := fmt.Fprintf(w, "%s%d\n", wrappedDirective, i); err != nil {
			return err
		}
	}
	for _, c := range Candidates(ctx, cfg, home) {
		if _, err := fmt.Fprintln(w, c); err != nil {
			return err
		}
	}
	return nil
}

// wrappedCandidates is the built-in fallback for a wrapped command's
// arguments. Only ssh destinations are known; everything else is left to
// the shell (bash-completion's delegation, or filename completion).
func wrappedCandidates(command string, args []string, home string) []string {
	if filepath.Base(command) != "ssh" || home == "" || !sshWantsDestination(args) {
		return nil
	}
	hosts, _ := sshconfig.Hosts(home) // partial results are still useful
	return hosts
}

// sshWantsDestination reports whether the word after args is ssh's
// destination: no destination has been typed yet and the previous word is
// not an option awaiting its value. Option clusters follow getopt, so in
// "-vp" the 'p' takes the next word, while in "-p22" the value is attached.
// Bash splits "user@host" at '@' by default (COMP_WORDBREAKS), so a
// destination followed by a lone "@" still means the host is being typed.
func sshWantsDestination(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return isUserAt(args[i+1:])
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return isUserAt(args[i:])
		}
		if sshconfig.OptionAwaitsValue(arg[1:]) {
			if i+1 == len(args) {
				return false // completing the option's value
			}
			i++
		}
	}
	return true
}

// isUserAt reports whether rest is empty or exactly "user", "@".
func isUserAt(rest []string) bool {
	return len(rest) == 0 || (len(rest) == 2 && rest[1] == "@")
}

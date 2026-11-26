package logstore

import (
	"bytes"
	"fmt"
	"time"
)

const (
	// HeaderTitle is the first line of every session log written by kuroko.
	HeaderTitle = "# kuroko session log"

	// SeparatorRule is the 68-dash divider used in session headers and footers.
	SeparatorRule = "--------------------------------------------------------------------"

	// CommandMetaPrefix introduces an internal command-boundary metadata line.
	CommandMetaPrefix = "# kuroko:cmd:"
)

var commandMetaPrefixBytes = []byte(CommandMetaPrefix)

// IsCommandMeta reports whether line (without trailing LF) is a kuroko
// command-boundary metadata line.
func IsCommandMeta(line []byte) bool {
	return bytes.HasPrefix(line, commandMetaPrefixBytes)
}

// AppendCommandMeta appends a "# kuroko:cmd:<RFC3339>\n" metadata line to dst.
func AppendCommandMeta(dst []byte, t time.Time) []byte {
	dst = append(dst, CommandMetaPrefix...)
	dst = t.AppendFormat(dst, time.RFC3339)
	return append(dst, '\n')
}

// FormatHeader formats the session log header block.
func FormatHeader(started time.Time, cmdLine, targetAddr string) string {
	return fmt.Sprintf(
		"%s\n# Started : %s\n# Command : %s\n# Target  : %s\n# %s\n\n",
		HeaderTitle,
		started.Format(time.RFC3339Nano),
		cmdLine,
		targetAddr,
		SeparatorRule,
	)
}

// FormatFooter formats the session log footer block.
func FormatFooter(ended time.Time, exitCode int) string {
	return fmt.Sprintf(
		"\n# %s\n# Ended   : %s\n# Exit    : %d\n",
		SeparatorRule,
		ended.Format(time.RFC3339Nano),
		exitCode,
	)
}

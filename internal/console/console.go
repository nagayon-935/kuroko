// Package console prints kuroko's own status messages to stderr, prefixed
// with "[kuroko]" and colored by severity: red for errors, yellow for
// warnings, and dim for informational notes.
//
// os.Stderr is looked up on every call so tests can redirect it.
package console

import (
	"fmt"
	"os"
)

const (
	red    = "\033[31m"
	yellow = "\033[33m"
	dim    = "\033[2m"
	reset  = "\033[0m"
)

// Errorf prints a red error message.
func Errorf(format string, args ...any) {
	printf(red, format, args...)
}

// Warnf prints a yellow warning message.
func Warnf(format string, args ...any) {
	printf(yellow, format, args...)
}

// Infof prints a dim informational message.
func Infof(format string, args ...any) {
	printf(dim, format, args...)
}

func printf(color, format string, args ...any) {
	fmt.Fprintf(os.Stderr, color+"[kuroko] "+format+reset+"\n", args...)
}

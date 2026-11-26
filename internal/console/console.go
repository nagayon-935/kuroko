// Package console prints kuroko's own status messages to stderr, prefixed
// with "[kuroko]" and colored by severity: red for errors, yellow for
// warnings, and dim for informational notes.
//
// os.Stderr is looked up on every call so tests can redirect it.
package console

import (
	"fmt"
	"io"
	"os"
)

const (
	red    = "\033[31m"
	yellow = "\033[33m"
	dim    = "\033[2m"
	reset  = "\033[0m"
)

// Errorf prints a red error message to os.Stderr.
func Errorf(format string, args ...any) {
	Ferrorf(os.Stderr, format, args...)
}

// Warnf prints a yellow warning message to os.Stderr.
func Warnf(format string, args ...any) {
	Fwarnf(os.Stderr, format, args...)
}

// Infof prints a dim informational message to os.Stderr.
func Infof(format string, args ...any) {
	Finfof(os.Stderr, format, args...)
}

// Ferrorf writes a red error message to w.
func Ferrorf(w io.Writer, format string, args ...any) {
	fprintf(w, red, format, args...)
}

// Fwarnf writes a yellow warning message to w.
func Fwarnf(w io.Writer, format string, args ...any) {
	fprintf(w, yellow, format, args...)
}

// Finfof writes a dim informational message to w.
func Finfof(w io.Writer, format string, args ...any) {
	fprintf(w, dim, format, args...)
}

func fprintf(w io.Writer, color, format string, args ...any) {
	fmt.Fprintf(w, color+"[kuroko] "+format+reset+"\n", args...)
}

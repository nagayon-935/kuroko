package console

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns what
// was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading pipe: %v", err)
	}
	return string(out)
}

func TestPrintersColorAndPrefixMessages(t *testing.T) {
	tests := []struct {
		name  string
		print func(string, ...any)
		want  string
	}{
		{"error is red", Errorf, "\033[31m[kuroko] boom: 42\033[0m\n"},
		{"warning is yellow", Warnf, "\033[33m[kuroko] boom: 42\033[0m\n"},
		{"info is dim", Infof, "\033[2m[kuroko] boom: 42\033[0m\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := captureStderr(t, func() { tt.print("boom: %d", 42) })
			if got != tt.want {
				t.Errorf("got %q; want %q", got, tt.want)
			}
		})
	}
}

func TestWriterPrintersTargetProvidedWriter(t *testing.T) {
	tests := []struct {
		name  string
		print func(io.Writer, string, ...any)
		want  string
	}{
		{"ferrorf", Ferrorf, "\033[31m[kuroko] msg: ok\033[0m\n"},
		{"fwarnf", Fwarnf, "\033[33m[kuroko] msg: ok\033[0m\n"},
		{"finfof", Finfof, "\033[2m[kuroko] msg: ok\033[0m\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			stderrOut := captureStderr(t, func() { tt.print(&buf, "msg: %s", "ok") })
			if stderrOut != "" {
				t.Errorf("unexpected os.Stderr output %q", stderrOut)
			}
			if buf.String() != tt.want {
				t.Errorf("got %q; want %q", buf.String(), tt.want)
			}
		})
	}
}

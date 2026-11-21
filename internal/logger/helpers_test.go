package logger

import (
	"os"
	"strings"
	"testing"
)

// logBody writes chunks to a fresh Logger for args and returns the logged
// rows between the header and the footer.
func logBody(t *testing.T, args []string, chunks ...string) []string {
	t.Helper()
	l, err := New(t.TempDir(), args, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if _, err := l.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(data), strings.Repeat("-", 68)+"\n\n")
	if !ok {
		t.Fatalf("log has no header:\n%s", data)
	}
	body, _, ok = strings.Cut(body, "\n# "+strings.Repeat("-", 68))
	if !ok {
		t.Fatalf("log has no footer:\n%s", data)
	}
	return strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}

// withoutTimestamps replaces command metadata lines by a fixed marker.
func withoutTimestamps(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		if strings.HasPrefix(r, "# kuroko:cmd:") {
			r = "# kuroko:cmd"
		}
		out[i] = r
	}
	return out
}

package viewer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScanLogsConcurrentKeepsAllItemsAndOrder(t *testing.T) {
	// Arrange: more logs than workers, with distinct start times.
	dir := t.TempDir()
	const n = 40
	for i := range n {
		body := fmt.Sprintf("# kuroko session log\n# Started : 2026-06-17T10:%02d:00+09:00\n# Command : ssh host%02d\n# ---\n\nout\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("log%02d.log", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &LogSelector{logDir: dir, sortDesc: true}

	// Act
	if err := s.scanLogs(); err != nil {
		t.Fatal(err)
	}

	// Assert: newest first, every item present with its own summary.
	if len(s.items) != n {
		t.Fatalf("got %d items, want %d", len(s.items), n)
	}
	for i, item := range s.items {
		idx := n - 1 - i
		if want := fmt.Sprintf("log%02d.log", idx); item.name != want {
			t.Fatalf("item %d = %s, want %s", i, item.name, want)
		}
		if want := fmt.Sprintf("host%02d", idx); item.summary.Target != want {
			t.Fatalf("item %d target = %q, want %q", i, item.summary.Target, want)
		}
	}
}

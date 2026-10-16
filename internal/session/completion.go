package session

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// Completion messages go to stderr, keeping wrapped stdout usable in pipes.
// Confirm the final path after compression and rotation before recommending it.
func writeCompletion(w io.Writer, path string, code int, duration time.Duration, closeErr error, failed bool) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if closeErr != nil || failed {
		fmt.Fprintf(w, "[kuroko] log may be incomplete → %s (exit %d)\n", path, code)
		return
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(w, "[kuroko] session ended (exit %d); log no longer retained after cleanup\n", code)
		} else {
			fmt.Fprintf(w, "[kuroko] cannot confirm saved log → %s: %v\n", path, err)
		}
		return
	}
	fmt.Fprintf(w, "[kuroko] saved → %s (exit %d, %s)\n[kuroko] view: kuroko view %s\n", path, code, duration.Round(time.Second), shellQuote(path))
}

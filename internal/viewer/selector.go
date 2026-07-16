package viewer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/textwidth"
)

type selectorItem struct {
	name    string
	modTime time.Time
	size    int64
}

type LogSelector struct {
	logDir      string
	items       []selectorItem
	filtered    []selectorItem
	selected    int
	searchQuery string
	inSearch    bool
	sortDesc    bool // true = newest first (default), false = oldest first
	width       int
	height      int
	listScroll  int
}

func RunSelector(cfg *config.Config) error {
	s, err := newSelector(cfg.LogDir)
	if err != nil {
		return err
	}
	return s.loop()
}

func newSelector(logDir string) (*LogSelector, error) {
	s := &LogSelector{
		logDir:   logDir,
		sortDesc: true, // newest first by default
	}
	if err := s.scanLogs(); err != nil {
		return nil, err
	}
	s.updateFilter()
	return s, nil
}

func (s *LogSelector) scanLogs() error {
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return fmt.Errorf("reading log dir: %w", err)
	}

	var items []selectorItem
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".log.gz") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		items = append(items, selectorItem{
			name:    name,
			modTime: info.ModTime(),
			size:    info.Size(),
		})
	}

	s.items = items
	s.applySortOrder(s.items)
	return nil
}

func (s *LogSelector) applySortOrder(items []selectorItem) {
	sort.Slice(items, func(i, j int) bool {
		if s.sortDesc {
			return items[i].modTime.After(items[j].modTime)
		}
		return items[i].modTime.Before(items[j].modTime)
	})
}

func (s *LogSelector) toggleSort() {
	s.sortDesc = !s.sortDesc
	s.applySortOrder(s.items)
	s.updateFilter()
	s.selected = 0
}

func (s *LogSelector) updateFilter() {
	s.filtered = nil
	query := strings.ToLower(s.searchQuery)
	for _, item := range s.items {
		if query == "" || strings.Contains(strings.ToLower(item.name), query) {
			s.filtered = append(s.filtered, item)
		}
	}
	if s.selected >= len(s.filtered) {
		s.selected = len(s.filtered) - 1
	}
	if s.selected < 0 {
		s.selected = 0
	}
}

func (s *LogSelector) loop() error {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Save screen, hide cursor, clear screen, and enable mouse tracking
	_, _ = os.Stdout.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[?1000h\x1b[?1006h"))
	defer func() {
		_, _ = os.Stdout.Write([]byte("\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[?1049l"))
	}()

	time.Sleep(50 * time.Millisecond)

	for {
		s.draw()

		buf := make([]byte, 16)
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}

		key := buf[:n]
		if bytes.HasPrefix(key, []byte("\x1b[<")) {
			m := mouseRegexp.FindSubmatch(key)
			if len(m) == 5 {
				btn, _ := strconv.Atoi(string(m[1]))
				y, _ := strconv.Atoi(string(m[3]))

				if btn == 64 { // Scroll Up
					if s.selected > 0 {
						s.selected--
					}
				} else if btn == 65 { // Scroll Down
					if s.selected < len(s.filtered)-1 {
						s.selected++
					}
				} else if btn == 0 { // Left click press
					r := y - 3
					if idx := clickRowToIndex(r, s.listScroll, len(s.filtered)); idx >= 0 {
						s.selected = idx
					}
				}
			}
			continue
		}

		if s.inSearch {
			if len(key) == 1 {
				b := key[0]
				switch b {
				case 13, 10: // Enter
					s.inSearch = false
				case 27: // Esc
					s.inSearch = false
				case 127, 8: // Backspace
					if len(s.searchQuery) > 0 {
						s.searchQuery = s.searchQuery[:len(s.searchQuery)-1]
						s.updateFilter()
					}
				case 3: // Ctrl+C
					return nil
				default:
					if b >= 32 && b < 127 {
						s.searchQuery += string(b)
						s.updateFilter()
					}
				}
			}
		} else {
			if len(key) == 1 {
				switch key[0] {
				case 'q', 27: // q or Esc
					return nil
				case 3: // Ctrl+C
					return nil
				case 'j': // Down
					if s.selected < len(s.filtered)-1 {
						s.selected++
					}
				case 'k': // Up
					if s.selected > 0 {
						s.selected--
					}
				case 's': // Toggle sort order
					s.toggleSort()
				case '/': // Filter mode
					s.inSearch = true
					s.searchQuery = ""
					s.updateFilter()
				case 13, 10: // Enter: View log
					if len(s.filtered) > 0 {
						selectedFile := s.filtered[s.selected]
						fullPath := filepath.Join(s.logDir, selectedFile.name)

						// Temporarily restore terminal state to run the sub-viewer.
						// The sub-viewer will set up raw mode itself.
						term.Restore(int(os.Stdin.Fd()), oldState)

						// Run viewer
						viewErr := Run(fullPath)

						// Re-enter raw mode and clear/save screen
						oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
						if err != nil {
							return err
						}
						// Clear screen and redraw selector. The sub-viewer disabled
						// mouse tracking on its own exit, so it must be re-enabled
						// here or wheel/click navigation stays dead for the rest of
						// the selector's lifetime.
						_, _ = os.Stdout.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[?1000h\x1b[?1006h"))
						time.Sleep(50 * time.Millisecond)
						if viewErr != nil {
							fmt.Fprintf(os.Stderr, "\r\n[kuroko] viewer error: %v\r\n", viewErr)
						}

						// Re-scan in case logs changed
						if serr := s.scanLogs(); serr != nil {
							fmt.Fprintf(os.Stderr, "\r\n[kuroko] rescan error: %v\r\n", serr)
						}
						s.updateFilter()
					}
				}
			} else if len(key) == 3 && key[0] == 27 && key[1] == '[' {
				switch key[2] {
				case 'A': // Up
					if s.selected > 0 {
						s.selected--
					}
				case 'B': // Down
					if s.selected < len(s.filtered)-1 {
						s.selected++
					}
				}
			}
		}
	}
}

// bodyHeight returns the number of rows available for the log list,
// reserving chromeHeight rows for the header, spacer, and footer.
func (s *LogSelector) bodyHeight() int {
	h := s.height - chromeHeight
	if h < 1 {
		h = 1
	}
	return h
}

func (s *LogSelector) draw() {
	s.width, s.height, _ = term.GetSize(int(os.Stdin.Fd()))
	if s.width <= 0 || s.height <= 0 {
		s.width = 80
		s.height = 24
	}

	var out bytes.Buffer
	out.WriteString("\x1b[H")

	bodyHeight := s.bodyHeight()
	s.listScroll = followSelection(s.selected, s.listScroll, bodyHeight)

	// Draw Header
	header := fmt.Sprintf(" kuroko logs selector  [ Dir: %s ]", s.logDir)
	if n := textwidth.String(header); n > s.width {
		header = truncateDisplay(header, s.width)
	} else {
		header += strings.Repeat(" ", s.width-n)
	}
	out.WriteString(fmt.Sprintf("\x1b[30;47m%s\x1b[0m\x1b[K\r\n", header))
	out.WriteString("\x1b[K\r\n")

	// Render items
	for r := 0; r < bodyHeight; r++ {
		var line string
		listRow := r + s.listScroll
		if listRow < len(s.filtered) {
			item := s.filtered[listRow]
			indicator := "  "
			if listRow == s.selected {
				indicator = "> "
			}

			// Format item line: modTime, size, name
			ts := item.modTime.Format("2006-01-02 15:04:05")
			sizeStr := formatSize(item.size)

			// Try to align nicely
			line = fmt.Sprintf("%s[%s] (%-8s) %s", indicator, ts, sizeStr, item.name)
			if n := textwidth.String(line); n > s.width {
				line = truncateDisplay(line, s.width)
			} else {
				line += strings.Repeat(" ", s.width-n)
			}

			if listRow == s.selected {
				line = fmt.Sprintf("\x1b[30;47m%s\x1b[0m", line)
			}
		} else {
			line = strings.Repeat(" ", s.width)
		}
		out.WriteString(line + "\x1b[K\r\n")
	}

	out.WriteString(fmt.Sprintf("\x1b[%d;1H", s.height))

	// Draw Footer
	sortLabel := "new→old"
	if !s.sortDesc {
		sortLabel = "old→new"
	}
	footerText := fmt.Sprintf(" [j/k/Arrows]: Navigate  [Enter]: View  [s]: Sort: %s  [/]: Filter  [q]: Quit", sortLabel)
	if s.inSearch {
		footerText = fmt.Sprintf(" Filter logs (Enter to confirm): %s_", s.searchQuery)
	}
	if n := textwidth.String(footerText); n > s.width {
		footerText = truncateDisplay(footerText, s.width)
	} else {
		footerText += strings.Repeat(" ", s.width-n)
	}
	out.WriteString(fmt.Sprintf("\x1b[30;47m%s\x1b[0m\x1b[K", footerText))

	os.Stdout.Write(out.Bytes())
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

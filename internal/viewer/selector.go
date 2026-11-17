package viewer

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/logger"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

type selectorItem struct {
	name      string
	modTime   time.Time
	size      int64
	summary   logstore.Summary
	readError bool
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
	// statusMessage is shown in the footer until the next key press. Errors
	// go here rather than to stderr, which the next frame would overwrite.
	statusMessage string
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
	entries, err := logstore.ListLogFiles(s.logDir)
	if err != nil {
		return err
	}

	// Summaries are read by a bounded worker pool. Each result is written to
	// its own slot, so the item order matches the entry order exactly.
	slots := make([]*selectorItem, len(entries))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), maxScanWorkers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				slots[i] = scanEntry(entries[i])
			}
		}()
	}
	for i := range entries {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var items []selectorItem
	for _, item := range slots {
		if item != nil {
			items = append(items, *item)
		}
	}

	s.items = items
	s.applySortOrder(s.items)
	return nil
}

const maxScanWorkers = 8

// scanEntry builds the list item for one log, or nil when its file info is
// unavailable (such entries are skipped).
func scanEntry(entry logstore.LogFile) *selectorItem {
	info, err := entry.Info()
	if err != nil {
		return nil
	}
	summary, readErr := logstore.ReadSummary(entry.Path)
	if summary.Target == "" && summary.Command != "" {
		summary.Target = logger.TargetAddress(strings.Fields(summary.Command))
	}
	return &selectorItem{
		name:      entry.RelativePath,
		modTime:   info.ModTime(),
		size:      info.Size(),
		summary:   summary,
		readError: readErr != nil,
	}
}

func (s *LogSelector) applySortOrder(items []selectorItem) {
	sort.Slice(items, func(i, j int) bool {
		ti, tj := items[i].started(), items[j].started()
		if ti.Equal(tj) {
			ti, tj = items[i].modTime, items[j].modTime
		}
		if ti.Equal(tj) {
			if s.sortDesc {
				return items[i].name > items[j].name
			}
			return items[i].name < items[j].name
		}
		if s.sortDesc {
			return ti.After(tj)
		}
		return ti.Before(tj)
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
		searchText := item.name + " " + item.summary.Target + " " + item.summary.Command + " " + item.status()
		if query == "" || strings.Contains(strings.ToLower(searchText), query) {
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

func (s *LogSelector) moveSelection(delta int) {
	next := s.selected + delta
	if next >= 0 && next < len(s.filtered) {
		s.selected = next
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

func (item selectorItem) started() time.Time {
	if !item.summary.Started.IsZero() {
		return item.summary.Started
	}
	return item.modTime
}

func (item selectorItem) status() string {
	if item.readError {
		return "unreadable"
	}
	if item.summary.ExitCode == nil {
		if !item.summary.Started.IsZero() {
			return "open"
		}
		return "unknown"
	}
	if *item.summary.ExitCode == 0 {
		return "OK"
	}
	return fmt.Sprintf("exit %d", *item.summary.ExitCode)
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

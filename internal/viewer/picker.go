package viewer

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/nagayon-935/kuroko/internal/logsearch"
	"github.com/nagayon-935/kuroko/internal/textwidth"
	"github.com/nagayon-935/kuroko/internal/tui"
)

// matchPicker is a full-screen list of grep matches. Enter opens the viewer
// on the selected match and returns here when the viewer quits.
type matchPicker struct {
	matches  []logsearch.Match
	capped   bool
	selected int
	scroll   int
	width    int
	height   int
	// statusMessage is shown in the footer until the next key press.
	statusMessage string
}

// pickerAction is what the loop must do after a key was handled.
type pickerAction int

const (
	pickerNone pickerAction = iota
	pickerOpen
	pickerQuit
)

// RunPicker lets the user choose among matches and open them in the viewer.
// capped says the list was cut short, which the footer notes.
func RunPicker(matches []logsearch.Match, capped bool) error {
	if len(matches) == 0 {
		return nil
	}
	return (&matchPicker{matches: matches, capped: capped}).loop()
}

func (p *matchPicker) loop() error {
	t, err := tui.Open()
	if err != nil {
		return err
	}
	defer t.Close()

	for {
		p.draw()

		key, err := tui.ReadKey()
		if err != nil {
			return err
		}
		if len(key) == 0 {
			continue
		}
		p.statusMessage = ""
		switch p.handleInput(key) {
		case pickerQuit:
			return nil
		case pickerOpen:
			if err := p.openSelected(t); err != nil {
				return err
			}
		}
	}
}

// handleInput applies a raw key read (single key, escape sequence or mouse
// report) to the picker state.
func (p *matchPicker) handleInput(key []byte) pickerAction {
	if m, ok := tui.ParseMouse(key); ok {
		return p.handleMouse(m)
	}
	if len(key) == 1 {
		switch key[0] {
		case 'q', tui.KeyEsc, tui.KeyCtrlC:
			return pickerQuit
		case 'j':
			p.move(1)
		case 'k':
			p.move(-1)
		case 'g':
			p.selected = 0
		case 'G':
			p.selected = len(p.matches) - 1
		case tui.KeyCtrlD:
			p.move(p.bodyHeight())
		case tui.KeyCtrlU:
			p.move(-p.bodyHeight())
		case tui.KeyCR, tui.KeyLF:
			return pickerOpen
		}
		return pickerNone
	}
	if tui.IsHomeKey(key) {
		p.selected = 0
		return pickerNone
	}
	if tui.IsEndKey(key) {
		p.selected = len(p.matches) - 1
		return pickerNone
	}
	if dir := tui.PageDir(key); dir != 0 {
		p.move(dir * p.bodyHeight())
		return pickerNone
	}
	if c, ok := tui.CSIKey(key); ok {
		switch c {
		case 'A':
			p.move(-1)
		case 'B':
			p.move(1)
		}
	}
	return pickerNone
}

func (p *matchPicker) handleMouse(m tui.Mouse) pickerAction {
	switch m.Button {
	case tui.MouseWheelUp:
		p.move(-1)
	case tui.MouseWheelDown:
		p.move(1)
	case tui.MouseLeft:
		if m.Release {
			return pickerNone
		}
		if idx := tui.ClickRowToIndex(m.Y-3, p.scroll, len(p.matches)); idx >= 0 {
			if idx == p.selected {
				return pickerOpen
			}
			p.selected = idx
		}
	}
	return pickerNone
}

// move shifts the selection by delta, clamped to the list.
func (p *matchPicker) move(delta int) {
	p.selected = max(0, min(p.selected+delta, len(p.matches)-1))
}

func (p *matchPicker) bodyHeight() int {
	return max(1, p.height-chromeHeight)
}

// openSelected hands the terminal to the viewer for the selected match and
// takes it back afterwards. A viewer failure is shown in the footer; only
// failing to reclaim the terminal is fatal.
func (p *matchPicker) openSelected(t *tui.Terminal) error {
	m := p.matches[p.selected]
	t.Suspend()
	viewErr := RunAtLine(m.Path, m.Line)
	if err := t.Resume(); err != nil {
		return err
	}
	if viewErr != nil {
		p.statusMessage = fmt.Sprintf("viewer error: %v", viewErr)
	}
	return nil
}

func (p *matchPicker) draw() {
	p.width, p.height = tui.Size()

	var out bytes.Buffer
	out.WriteString(tui.Home)

	bodyHeight := p.bodyHeight()
	p.scroll = tui.FollowSelection(p.selected, p.scroll, bodyHeight)

	header := fmt.Sprintf(" kuroko grep results  [ %d matches ]", len(p.matches))
	out.WriteString(tui.Inverse(textwidth.Fit(header, p.width)) + tui.ClearEOL + "\r\n")
	out.WriteString(tui.ClearEOL + "\r\n")
	for r := 0; r < bodyHeight; r++ {
		out.WriteString(p.rowText(r) + tui.ClearEOL + "\r\n")
	}
	out.WriteString(tui.MoveToRow(p.height))
	out.WriteString(tui.Inverse(textwidth.Fit(p.footerText(), p.width)) + tui.ClearEOL)

	os.Stdout.Write(out.Bytes())
}

// rowText renders body row r as "relpath:line:text", exactly p.width columns.
func (p *matchPicker) rowText(r int) string {
	row := r + p.scroll
	if row >= len(p.matches) {
		return strings.Repeat(" ", p.width)
	}
	m := p.matches[row]
	indicator := "  "
	if row == p.selected {
		indicator = "> "
	}
	line := textwidth.Fit(indicator+tui.Plain(fmt.Sprintf("%s:%d:%s", m.RelativePath, m.Line, m.Text)), p.width)
	if row == p.selected {
		line = tui.Inverse(line)
	}
	return line
}

func (p *matchPicker) footerText() string {
	if p.statusMessage != "" {
		return " " + tui.Plain(p.statusMessage)
	}
	note := ""
	if p.capped {
		note = fmt.Sprintf("  (showing the first %d matches only)", len(p.matches))
	}
	return fmt.Sprintf(" %d/%d  [j/k/Arrows/PgUp/PgDn/g/G]: Move  [Enter]: Open  [q]: Quit%s", p.selected+1, len(p.matches), note)
}

// Package launcher provides interactive connection setup for a bare kuroko invocation.
package launcher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryu/kuroko/internal/tui"
	"golang.org/x/term"
)

var (
	errCancelled = errors.New("cancelled")
	errBack      = errors.New("back")
)

// Run returns command arguments ready for session.New, or nil on cancellation.
func Run() ([]string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("interactive selection requires a terminal; specify a command, e.g. kuroko ssh host")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer term.Restore(fd, state)
	fmt.Fprint(os.Stderr, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(os.Stderr, "\x1b[?25h\x1b[?1049l")
	args, err := choose(os.Stdin, os.Stderr, home, "/dev")
	if errors.Is(err, errCancelled) {
		return nil, nil
	}
	return args, err
}

func choose(in io.Reader, out io.Writer, home, devRoot string) ([]string, error) {
	p := prompt{in: in, out: out}
	devices, err := serialDevices(devRoot)
	if err != nil {
		return nil, err
	}
	methods := []string{"ssh"}
	kind := "ssh"
	if len(devices) > 0 {
		methods = append(methods, "screen")
		kind = ""
	}
	for {
		if kind == "" {
			p.clear()
			kind, err = p.selectItem("Connection", methods, false, false)
			if err != nil {
				return nil, err
			}
		}
		var args []string
		if kind == "ssh" {
			args, err = p.ssh(home)
		} else {
			args, err = p.screen(devices)
		}
		if errors.Is(err, errBack) {
			kind = ""
			continue
		}
		if err == nil {
			p.clear()
		}
		return args, err
	}
}

func (p *prompt) ssh(home string) ([]string, error) {
	hosts, previews, readErr := sshEntries(home)
	p.previews = previews
	if readErr != nil {
		p.notice = fmt.Sprintf("Could not read all SSH hosts: %v", readErr)
	}
	defer func() { p.previews, p.notice = nil, "" }()
	for {
		p.clear()
		host, err := p.selectItem("SSH host", hosts, true, true)
		if err != nil {
			return nil, err
		}
		for !validHost(host) {
			fmt.Fprint(p.out, "Enter a host or user@host without spaces or a leading '-'.\r\n")
			host, err = p.line("SSH host (Ctrl+B: back, Ctrl+C: cancel): ")
			if err != nil {
				break
			}
		}
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return []string{"ssh", host}, nil
	}
}

func (p *prompt) screen(devices []string) ([]string, error) {
	for {
		p.clear()
		device, err := p.selectItem("Serial device", devices, true, true)
		if err != nil {
			return nil, err
		}
		for !validDevice(device) {
			fmt.Fprint(p.out, "Enter an absolute path to an existing character device.\r\n")
			device, err = p.line("Serial device (Ctrl+B: back, Ctrl+C: cancel): ")
			if err != nil {
				break
			}
		}
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			return nil, err
		}
		p.clear()
		fmt.Fprintf(p.out, "Serial device: %s\r\n", tui.Plain(device))
		baud, err := p.promptBaudRate()
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return []string{"screen", device, baud}, nil
	}
}

func (p *prompt) promptBaudRate() (string, error) {
	for {
		baud, err := p.line("Baud rate [115200] (Ctrl+B: back, Ctrl+C: cancel): ")
		if err != nil {
			return "", err
		}
		if baud == "" {
			baud = "115200"
		}
		n, err := strconv.ParseUint(baud, 10, 32)
		if err == nil && n > 0 {
			return strconv.FormatUint(n, 10), nil
		}
		fmt.Fprint(p.out, "Enter a positive numeric baud rate.\r\n")
	}
}

func validHost(host string) bool {
	return host != "" &&
		utf8.ValidString(host) &&
		!strings.HasPrefix(host, "-") &&
		!strings.ContainsAny(host, "*?!") &&
		!strings.ContainsFunc(host, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r) || !unicode.IsPrint(r)
		})
}

func validDevice(device string) bool {
	if !strings.HasPrefix(device, "/") || !utf8.ValidString(device) {
		return false
	}
	if strings.ContainsFunc(device, func(r rune) bool { return unicode.IsControl(r) || !unicode.IsPrint(r) }) {
		return false
	}
	info, err := os.Stat(device)
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type prompt struct {
	in       io.Reader
	out      io.Writer
	previews map[string]string
	notice   string
}

func (p *prompt) clear() {
	fmt.Fprint(p.out, "\x1b[2J\x1b[H")
}

type menuState struct {
	selected      int
	scroll        int
	previewScroll int
	query         string
	escape        int
	sequence      string
}

func (m *menuState) applySequence(seq string, filteredCount, pageStep int) {
	switch seq {
	case "A":
		m.selected = max(0, m.selected-1)
		m.previewScroll = 0
	case "B":
		m.selected = min(max(0, filteredCount-1), m.selected+1)
		m.previewScroll = 0
	case "5~":
		m.previewScroll = max(0, m.previewScroll-pageStep)
	case "6~":
		m.previewScroll += pageStep
	}
}

func (p *prompt) selectItem(title string, items []string, manual, back bool) (string, error) {
	var st menuState
	for {
		filtered := filterItems(items, st.query)
		if st.selected >= len(filtered) {
			st.selected = max(0, len(filtered)-1)
		}
		width, height := tui.Size()
		st.scroll = tui.FollowSelection(st.selected, st.scroll, listBodyHeight(width, height))
		if st.escape == 0 {
			st.previewScroll = p.drawMenu(title, filtered, st.selected, st.scroll, st.previewScroll, width, height, st.query, manual, back)
		}
		key, err := p.readByte()
		if err != nil {
			return "", err
		}
		if key == tui.KeyCtrlC || key == tui.KeyCtrlD {
			return "", errCancelled
		}
		if st.escape != 0 {
			var doneSeq string
			var consumed bool
			st.escape, st.sequence, doneSeq, consumed = stepEscape(st.escape, st.sequence, key)
			if consumed {
				if doneSeq != "" {
					st.applySequence(doneSeq, len(filtered), max(1, height-5))
				}
				continue
			}
		}
		item, done, err := p.handleMenuKey(&st, title, filtered, key, manual, back)
		if done || err != nil {
			return item, err
		}
	}
}

func (p *prompt) handleMenuKey(st *menuState, title string, filtered []string, key byte, manual, back bool) (string, bool, error) {
	switch key {
	case tui.KeyEsc:
		st.escape = 1
		return "", false, nil
	case tui.KeyCtrlB:
		if back {
			return "", true, errBack
		}
		return "", false, nil
	case tui.KeyCtrlN:
		if !manual {
			return "", false, nil
		}
		p.clear()
		value, err := p.line(title + " (Ctrl+B: back, Ctrl+C: cancel): ")
		if errors.Is(err, errBack) {
			return "", false, nil
		}
		return value, true, err
	case tui.KeyCR, tui.KeyLF:
		if len(filtered) > 0 {
			return filtered[st.selected], true, nil
		}
		return "", false, nil
	case tui.KeyCtrlU:
		st.query = ""
	case tui.KeyBackspace, tui.KeyDelete:
		if len(st.query) > 0 {
			_, n := utf8.DecodeLastRuneInString(st.query)
			st.query = st.query[:len(st.query)-n]
		}
	default:
		if key < 32 {
			return "", false, nil
		}
		st.query += string([]byte{key})
	}
	st.selected, st.scroll, st.previewScroll = 0, 0, 0
	return "", false, nil
}

// stepEscape advances the CSI/SS3 escape state machine by one byte.
// When consumed is false, the byte after a bare ESC was not '[' or 'O' and
// should be handled as a normal key press.
func stepEscape(escape int, sequence string, key byte) (nextEscape int, nextSeq, doneSeq string, consumed bool) {
	if escape == 1 {
		if key == '[' || key == 'O' {
			return 2, "", "", true
		}
		return 0, "", "", false
	}
	sequence += string(key)
	if key < '@' || key > '~' {
		if len(sequence) > 16 {
			return 0, "", "", true
		}
		return 2, sequence, "", true
	}
	return 0, "", sequence, true
}

func filterItems(items []string, query string) []string {
	var filtered []string
	lowerQuery := strings.ToLower(query)
	for _, item := range items {
		if fuzzyMatch(item, lowerQuery) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func fuzzyMatch(item, lowerQuery string) bool {
	rest := []rune(strings.ToLower(item))
	for _, r := range lowerQuery {
		found := false
		for len(rest) > 0 {
			c := rest[0]
			rest = rest[1:]
			if c == r {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (p *prompt) readByte() (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(p.in, b[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, errCancelled
		}
		return 0, err
	}
	return b[0], nil
}

func (p *prompt) line(label string) (string, error) {
	var value []byte
	escape := 0
	sequence := ""
	for {
		if escape == 0 {
			fmt.Fprintf(p.out, "\r\x1b[2K%s%s", label, tui.Plain(string(value)))
		}
		key, err := p.readByte()
		if err != nil {
			return "", err
		}
		if key == tui.KeyCtrlC || key == tui.KeyCtrlD {
			return "", errCancelled
		}
		if escape != 0 {
			var consumed bool
			escape, sequence, _, consumed = stepEscape(escape, sequence, key)
			if consumed {
				continue
			}
		}
		switch key {
		case tui.KeyEsc:
			escape = 1
		case tui.KeyCtrlB:
			return "", errBack
		case tui.KeyCR, tui.KeyLF:
			fmt.Fprint(p.out, "\r\n")
			return strings.TrimSpace(string(value)), nil
		case tui.KeyBackspace, tui.KeyDelete:
			if len(value) > 0 {
				_, n := utf8.DecodeLastRune(value)
				value = value[:len(value)-n]
			}
		case tui.KeyCtrlU:
			value = nil
		default:
			if key >= 32 {
				value = append(value, key)
			}
		}
	}
}

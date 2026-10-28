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
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, fmt.Errorf("interactive selection requires a terminal; specify a command, e.g. kuroko ssh host")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	defer term.Restore(int(os.Stdin.Fd()), state)
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
		for {
			info, statErr := os.Stat(device)
			if strings.HasPrefix(device, "/") && statErr == nil && info.Mode()&os.ModeCharDevice != 0 {
				break
			}
			fmt.Fprintln(p.out, "Enter an absolute path to an existing character device.")
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
		fmt.Fprintf(p.out, "Serial device: %s\n", device)
		for {
			baud, err := p.line("Baud rate [115200] (Ctrl+B: back, Ctrl+C: cancel): ")
			if errors.Is(err, errBack) {
				break
			}
			if err != nil {
				return nil, err
			}
			if baud == "" {
				baud = "115200"
			}
			n, err := strconv.ParseUint(baud, 10, 32)
			if err == nil && n > 0 {
				return []string{"screen", device, strconv.FormatUint(n, 10)}, nil
			}
			fmt.Fprintln(p.out, "Enter a positive numeric baud rate.")
		}
	}
}

func validHost(host string) bool {
	return host != "" && !strings.HasPrefix(host, "-") &&
		!strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
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

func (p *prompt) selectItem(title string, items []string, manual, back bool) (string, error) {
	selected, scroll, previewScroll := 0, 0, 0
	query, sequence := "", ""
	escape := 0
	for {
		filtered := filterItems(items, query)
		if selected >= len(filtered) {
			selected = max(0, len(filtered)-1)
		}
		width, height := tui.Size()
		scroll = tui.FollowSelection(selected, scroll, max(1, height-5))
		if escape == 0 {
			previewScroll = p.drawMenu(title, filtered, selected, scroll, previewScroll, width, height, query, manual, back)
		}
		key, err := p.readByte()
		if err != nil {
			return "", err
		}
		if key == 3 || key == 4 {
			return "", errCancelled
		}
		if escape == 1 {
			if key == '[' || key == 'O' {
				escape = 2
				sequence = ""
				continue
			}
			escape = 0
		} else if escape == 2 {
			sequence += string(key)
			if key < '@' || key > '~' {
				if len(sequence) > 16 {
					escape = 0
				}
				continue
			}
			escape = 0
			switch sequence {
			case "A":
				selected = max(0, selected-1)
				previewScroll = 0
			case "B":
				selected = min(max(0, len(filtered)-1), selected+1)
				previewScroll = 0
			case "5~":
				previewScroll = max(0, previewScroll-max(1, height-5))
			case "6~":
				previewScroll += max(1, height-5)
			}
			continue
		}
		switch key {
		case 27:
			escape = 1
			continue
		case 2:
			if back {
				return "", errBack
			}
			continue
		case 14:
			if manual {
				p.clear()
				value, err := p.line(title + " (Ctrl+B: back, Ctrl+C: cancel): ")
				if errors.Is(err, errBack) {
					continue
				}
				return value, err
			}
			continue
		case '\r', '\n':
			if len(filtered) > 0 {
				return filtered[selected], nil
			}
			continue
		case 21:
			query = ""
		case 8, 127:
			if len(query) > 0 {
				_, n := utf8.DecodeLastRuneInString(query)
				query = query[:len(query)-n]
			}
		default:
			if key >= 32 {
				query += string([]byte{key})
			} else {
				continue
			}
		}
		selected, scroll, previewScroll = 0, 0, 0
	}
}

func filterItems(items []string, query string) []string {
	var filtered []string
	for _, item := range items {
		rest := []rune(strings.ToLower(item))
		matches := true
		for _, r := range strings.ToLower(query) {
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
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, item)
		}
	}
	return filtered
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
	for {
		fmt.Fprintf(p.out, "\r\x1b[2K%s%s", label, value)
		key, err := p.readByte()
		if err != nil {
			return "", err
		}
		switch key {
		case 2:
			return "", errBack
		case 3, 4:
			return "", errCancelled

		case '\r', '\n':
			fmt.Fprint(p.out, "\r\n")
			return strings.TrimSpace(string(value)), nil
		case 8, 127:
			if len(value) > 0 {
				_, n := utf8.DecodeLastRune(value)
				value = value[:len(value)-n]
			}
		case 21:
			value = nil
		default:
			if key >= 32 {
				value = append(value, key)
			}
		}
	}
}

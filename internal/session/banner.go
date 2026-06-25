package session

import (
	"fmt"
	"io"
	"strings"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logger"
)

// ansiColor maps color name strings to ANSI foreground escape codes.
var ansiColor = map[string]string{
	"red":     "\x1b[31m",
	"yellow":  "\x1b[33m",
	"green":   "\x1b[32m",
	"cyan":    "\x1b[36m",
	"blue":    "\x1b[34m",
	"magenta": "\x1b[35m",
}

const ansiReset = "\x1b[0m"
const ansiBold = "\x1b[1m"

// writeBanner renders the session-start banner to w.
// It is a package-level function (not method) to keep it easily testable.
func writeBanner(w io.Writer, args []string, cfg *config.Config) {
	if !cfg.Banner.Enabled {
		return
	}

	address, hostname := logger.TargetDetails(args)
	if address == "" && len(args) > 0 {
		address = args[0]
		hostname = args[0]
	}

	// Find the first matching rule against the full address (case-insensitive).
	var matchedRule *config.BannerRule
	lowerAddr := strings.ToLower(address)
	lowerHost := strings.ToLower(hostname)
	for i := range cfg.Banner.Rules {
		pat := strings.ToLower(cfg.Banner.Rules[i].Match)
		if strings.Contains(lowerAddr, pat) || strings.Contains(lowerHost, pat) {
			matchedRule = &cfg.Banner.Rules[i]
			break
		}
	}

	if matchedRule != nil {
		color := ansiColor[matchedRule.Color]
		if color == "" {
			color = ansiColor["yellow"]
		}
		renderColoredBanner(w, address, hostname, matchedRule.Label, color)
	} else {
		renderPlainBanner(w, address, hostname)
	}
}

// displayWidth returns the number of terminal columns that string s occupies.
// CJK wide characters (Japanese, Chinese, Korean etc.) count as 2 columns.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// isWideRune reports whether r is a CJK or other double-width Unicode character.
func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2E80 && r <= 0x303F) ||
		(r >= 0x3040 && r <= 0x33FF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xA000 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7AF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2CEAF)
}

// pad right-pads s with spaces so that its display width equals targetWidth.
func pad(s string, targetWidth int) string {
	w := displayWidth(s)
	if w < targetWidth {
		return s + strings.Repeat(" ", targetWidth-w)
	}
	return s
}

// buildLines returns the content lines to show inside the banner box.
// Username (user@) is always stripped; only host/IP is displayed.
// Line 1: ホスト when connecting by name, IPアドレス when connecting by IP directly.
// Line 2: IPアドレス or ホスト when ssh -G resolved the alias to something different.
func buildLines(address, hostname string) []string {
	// Strip user@ from address for display — username is not shown in the banner.
	hostInAddr := address
	if idx := strings.LastIndex(address, "@"); idx >= 0 {
		hostInAddr = address[idx+1:]
	}

	var line1 string
	if isIPAddress(hostInAddr) {
		line1 = fmt.Sprintf("  IPアドレス: %s", hostInAddr)
	} else {
		line1 = fmt.Sprintf("  ホスト    : %s", hostInAddr)
	}
	lines := []string{line1}

	// Second line only when ssh -G resolved the alias to a different value.
	if hostname != "" && hostname != hostInAddr {
		if isIPAddress(hostname) {
			lines = append(lines, fmt.Sprintf("  IPアドレス: %s", hostname))
		} else {
			lines = append(lines, fmt.Sprintf("  ホスト    : %s", hostname))
		}
	}
	return lines
}

// isIPAddress reports whether s looks like an IPv4 or IPv6 address.
func isIPAddress(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') &&
			(r < 'a' || r > 'f') &&
			(r < 'A' || r > 'F') &&
			r != '.' && r != ':' {
			return false
		}
	}
	// Must contain at least one separator to distinguish from plain hex words.
	return strings.ContainsAny(s, ".:")
}

func renderPlainBanner(w io.Writer, address, hostname string) {
	lines := buildLines(address, hostname)

	innerWidth := 0
	for _, l := range lines {
		if dw := displayWidth(l); dw > innerWidth {
			innerWidth = dw
		}
	}
	innerWidth += 2 // one space padding on each side
	border := strings.Repeat("─", innerWidth)

	fmt.Fprintf(w, "┌%s┐\n", border)
	for _, l := range lines {
		fmt.Fprintf(w, "│ %s │\n", pad(l, innerWidth-2))
	}
	fmt.Fprintf(w, "└%s┘\n", border)
}

func renderColoredBanner(w io.Writer, address, hostname, label, color string) {
	lines := buildLines(address, hostname)
	labelLine := fmt.Sprintf("  [%s]", label)

	innerWidth := displayWidth(labelLine)
	for _, l := range lines {
		if dw := displayWidth(l); dw > innerWidth {
			innerWidth = dw
		}
	}
	innerWidth += 2
	border := strings.Repeat("═", innerWidth)

	fmt.Fprintf(w, "%s%s╔%s╗%s\n", color, ansiBold, border, ansiReset)
	for _, l := range lines {
		fmt.Fprintf(w, "%s%s║ %s ║%s\n", color, ansiBold, pad(l, innerWidth-2), ansiReset)
	}
	fmt.Fprintf(w, "%s%s║ %s ║%s\n", color, ansiBold, pad(labelLine, innerWidth-2), ansiReset)
	fmt.Fprintf(w, "%s%s╚%s╝%s\n", color, ansiBold, border, ansiReset)
}

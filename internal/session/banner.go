package session

import (
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/nagayon-935/kuroko/internal/config"
	"github.com/nagayon-935/kuroko/internal/logger"
	"github.com/nagayon-935/kuroko/internal/textwidth"
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
func displayWidth(s string) int {
	return textwidth.String(s)
}

// pad right-pads s with spaces so that its display width equals targetWidth.
func pad(s string, targetWidth int) string {
	w := displayWidth(s)
	if w < targetWidth {
		return s + strings.Repeat(" ", targetWidth-w)
	}
	return s
}

func formatHostLine(host string) string {
	if isIPAddress(host) {
		return fmt.Sprintf("  IPアドレス: %s", host)
	}
	return fmt.Sprintf("  ホスト    : %s", host)
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

	lines := []string{formatHostLine(hostInAddr)}

	// Second line only when ssh -G resolved the alias to a different value.
	if hostname != "" && hostname != hostInAddr {
		lines = append(lines, formatHostLine(hostname))
	}
	return lines
}

// isIPAddress reports whether s is a valid IPv4 or IPv6 address.
func isIPAddress(s string) bool {
	return net.ParseIP(s) != nil
}

func maxDisplayWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if dw := displayWidth(l); dw > w {
			w = dw
		}
	}
	return w
}

func renderPlainBanner(w io.Writer, address, hostname string) {
	lines := buildLines(address, hostname)
	innerWidth := maxDisplayWidth(lines) + 2 // one space padding on each side
	border := strings.Repeat("─", innerWidth)

	fmt.Fprintf(w, "┌%s┐\n", border)
	for _, l := range lines {
		fmt.Fprintf(w, "│ %s │\n", pad(l, innerWidth-2))
	}
	fmt.Fprintf(w, "└%s┘\n", border)
}

func renderColoredBanner(w io.Writer, address, hostname, label, color string) {
	lines := append(buildLines(address, hostname), fmt.Sprintf("  [%s]", label))
	innerWidth := maxDisplayWidth(lines) + 2
	border := strings.Repeat("═", innerWidth)

	fmt.Fprintf(w, "%s%s╔%s╗%s\n", color, ansiBold, border, ansiReset)
	for _, l := range lines {
		fmt.Fprintf(w, "%s%s║ %s ║%s\n", color, ansiBold, pad(l, innerWidth-2), ansiReset)
	}
	fmt.Fprintf(w, "%s%s╚%s╝%s\n", color, ansiBold, border, ansiReset)
}

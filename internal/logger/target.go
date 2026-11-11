package logger

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nagayon-935/kuroko/internal/sshconfig"
)

// TargetDetails returns two values:
//   - address: full connection target as typed (e.g. "admin@router-a")
//   - hostname: resolved canonical hostname (e.g. "router-a.dc1.example.jp")
//
// For non-SSH commands both values are identical.
// The banner uses both values so operators see what they typed AND the resolved host.
func TargetDetails(args []string) (address, hostname string) {
	if len(args) == 0 {
		return "", ""
	}
	raw := TargetAddress(args)
	switch filepath.Base(args[0]) {
	case "ssh":
		resolved := resolveSSHHostname(raw) // may resolve SSH config alias
		// hostname is the bare host part of the resolved target
		h := resolved
		if idx := strings.LastIndex(h, "@"); idx >= 0 {
			h = h[idx+1:]
		}
		return raw, h
	default:
		return raw, raw
	}
}

// TargetAddress extracts the typed target without starting another process.
// Filename generation only needs this value; alias resolution is for banners.
func TargetAddress(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch filepath.Base(args[0]) {
	case "ssh":
		return extractSSHTarget(args[1:])
	case "screen":
		return extractScreenTarget(args[1:])
	default:
		return args[0]
	}
}

func generateFilename(args []string) string {
	ts := time.Now().Format("20060102_150405")
	if len(args) == 0 {
		return fmt.Sprintf("%s_unknown.log", ts)
	}

	cmd := filepath.Base(args[0])
	// Use the typed host (address without user@) as the filename component so
	// SSH aliases like "edgeSW03" are preserved instead of being replaced by
	// the resolved IP from ssh -G.
	host := TargetAddress(args)
	if idx := strings.LastIndex(host, "@"); idx >= 0 {
		host = host[idx+1:]
	}

	if host != "" && host != cmd {
		return fmt.Sprintf("%s_%s_%s.log", ts, sanitize(cmd), sanitize(host))
	}
	return fmt.Sprintf("%s_%s.log", ts, sanitize(cmd))
}

const sshResolveTimeout = 2 * time.Second

// resolveSSHHostname runs "ssh -G -- <host>" to resolve an alias defined in
// ~/.ssh/config to its actual HostName. Returns the original target if
// resolution fails or if the hostname is already the canonical name.
func resolveSSHHostname(target string) string {
	if target == "" || !sshconfig.ValidHost(target) {
		return target
	}

	user, host := "", target
	if idx := strings.LastIndex(target, "@"); idx >= 0 {
		user = target[:idx+1]
		host = target[idx+1:]
	}
	if !sshconfig.ValidHost(host) {
		return target
	}

	ctx, cancel := context.WithTimeout(context.Background(), sshResolveTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-G", "--", host).Output()
	if err != nil {
		return target
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.ToLower(line), "hostname ") {
			resolved := strings.TrimSpace(line[len("hostname "):])
			if resolved != "" && resolved != host && sshconfig.ValidHost(resolved) {
				return user + resolved
			}
			break
		}
	}
	return target
}

// extractSSHTarget returns the first non-flag argument (user@host or host).
func extractSSHTarget(args []string) string {
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if sshconfig.OptionAwaitsValue(arg[1:]) {
				skipNext = true
			}
			continue
		}
		return arg
	}
	return ""
}

// extractScreenTarget returns the device basename (e.g. ttyUSB0 from /dev/ttyUSB0).
func extractScreenTarget(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			parts := strings.Split(arg, "/")
			return parts[len(parts)-1]
		}
	}
	return ""
}

const maxFilenamePartBytes = 96

func sanitize(s string) string {
	r := strings.NewReplacer(
		"/", "_", ":", "_", " ", "_", "\\", "_",
		"*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	out := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return '_'
		}
		return c
	}, r.Replace(s))
	if len(out) > maxFilenamePartBytes {
		out = out[:maxFilenamePartBytes]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

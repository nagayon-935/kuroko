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
		resolved := raw
		if opts, ok := extractSSHResolveOptions(args[1:]); ok {
			resolved = resolveSSHHostname(raw, opts)
		}
		// hostname is the bare host part of the resolved target
		_, h := splitUserHost(resolved)
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
	_, host := splitUserHost(TargetAddress(args))

	if host != "" && host != cmd {
		return fmt.Sprintf("%s_%s_%s.log", ts, sanitize(cmd), sanitize(host))
	}
	return fmt.Sprintf("%s_%s.log", ts, sanitize(cmd))
}

// splitUserHost splits "user@host" into "user@" (with the separator, so
// user+host reassembles the target) and "host". A target without "@" has an
// empty user part.
func splitUserHost(target string) (user, host string) {
	if idx := strings.LastIndex(target, "@"); idx >= 0 {
		return target[:idx+1], target[idx+1:]
	}
	return "", target
}

const sshResolveTimeout = 2 * time.Second

// resolveSSHHostname runs "ssh -G <opts> -- <host>" to resolve an alias
// using the supplied connection options. Returns the original target if
// resolution fails or if the hostname is already the canonical name.
func resolveSSHHostname(target string, opts []string) string {
	if target == "" || !sshconfig.ValidHost(target) {
		return target
	}

	user, host := splitUserHost(target)
	if !sshconfig.ValidHost(host) {
		return target
	}

	ctx, cancel := context.WithTimeout(context.Background(), sshResolveTimeout)
	defer cancel()
	args := append([]string{"-G"}, opts...)
	args = append(args, "--", host)
	out, err := exec.CommandContext(ctx, "ssh", args...).Output()
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

// extractSSHResolveOptions collects connection options before the destination.
func extractSSHResolveOptions(args []string) ([]string, bool) {
	var opts []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		cluster := arg[1:]
		index := sshconfig.ValueOptionIndex(cluster)
		flags := cluster
		if index >= 0 {
			flags = cluster[:index]
		}
		for _, flag := range flags {
			if flag == '4' || flag == '6' {
				opts = append(opts, "-"+string(flag))
			}
		}
		if index < 0 {
			continue
		}

		option := cluster[index : index+1]
		value := cluster[index+1:]
		if value == "" {
			i++
			if i == len(args) {
				return nil, false
			}
			value = args[i]
		}
		if len(value) > 4096 || strings.IndexByte(value, 0) >= 0 {
			return nil, false
		}
		if strings.Contains("FolpJ", option) {
			opts = append(opts, "-"+option, value)
		}
	}
	return opts, true
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

var filenameReplacer = strings.NewReplacer(
	"/", "_", ":", "_", " ", "_", "\\", "_",
	"*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
)

func sanitize(s string) string {
	out := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return '_'
		}
		return c
	}, filenameReplacer.Replace(s))
	if len(out) > maxFilenamePartBytes {
		out = out[:maxFilenamePartBytes]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

package logger

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// TargetName returns the human-readable target name for the given command
// arguments (e.g. hostname for ssh, device for screen, command name otherwise).
// Alias resolution is included; filenames use the typed address instead.
func TargetName(args []string) string {
	_, hostname := TargetDetails(args)
	return hostname
}

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
	raw := targetAddress(args)
	switch args[0] {
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

// targetAddress extracts the typed target without starting another process.
// Filename generation only needs this value; alias resolution is for banners.
func targetAddress(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
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

	cmd := args[0]
	// Use the typed host (address without user@) as the filename component so
	// SSH aliases like "edgeSW03" are preserved instead of being replaced by
	// the resolved IP from ssh -G.
	host := targetAddress(args)
	if idx := strings.LastIndex(host, "@"); idx >= 0 {
		host = host[idx+1:]
	}

	if host != "" && host != cmd {
		return fmt.Sprintf("%s_%s_%s.log", ts, cmd, sanitize(host))
	}
	return fmt.Sprintf("%s_%s.log", ts, sanitize(cmd))
}

// resolveSSHHostname runs "ssh -G <host>" to resolve an alias defined in
// ~/.ssh/config to its actual HostName. Returns the original target if
// resolution fails or if the hostname is already the canonical name.
func resolveSSHHostname(target string) string {
	if target == "" {
		return target
	}

	user, host := "", target
	if idx := strings.LastIndex(target, "@"); idx >= 0 {
		user = target[:idx+1]
		host = target[idx+1:]
	}

	out, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		return target
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.ToLower(line), "hostname ") {
			resolved := strings.TrimSpace(line[len("hostname "):])
			if resolved != "" && resolved != host {
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
	// SSH options that consume the next argument
	sshOptionArgs := map[string]bool{
		"-b": true, "-c": true, "-D": true, "-E": true, "-e": true,
		"-F": true, "-I": true, "-i": true, "-J": true, "-L": true,
		"-l": true, "-m": true, "-o": true, "-p": true, "-Q": true,
		"-R": true, "-S": true, "-w": true, "-W": true,
	}
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if sshOptionArgs[arg] {
			skipNext = true
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
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

func sanitize(s string) string {
	r := strings.NewReplacer(
		"/", "_", ":", "_", " ", "_", "\\", "_",
		"*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	return r.Replace(s)
}

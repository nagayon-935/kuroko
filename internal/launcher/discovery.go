package launcher

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sshHosts collects literal aliases, without evaluating Match exec or running SSH.
// Includes are scanned as candidate sources even inside conditional blocks;
// SSH itself remains responsible for evaluating the selected host's settings.
func sshHosts(home string) ([]string, error) {
	hosts, _, err := sshEntries(home)
	return hosts, err
}

func sshEntries(home string) ([]string, map[string]string, error) {
	previews := map[string]string{}
	var hosts []string
	seenFiles, seenHosts := map[string]bool{}, map[string]bool{}
	var read func(string) error
	read = func(path string) error {
		path = filepath.Clean(path)
		if seenFiles[path] {
			return nil
		}
		seenFiles[path] = true
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		var active []string
		var block []string
		lineNumber, start := 0, 0
		flush := func() {
			for _, host := range active {
				if previews[host] != "" {
					previews[host] += "\n\n"
				}
				previews[host] += fmt.Sprintf("%s:%d\n%s", path, start, strings.Join(block, "\n"))
			}
		}
		for s.Scan() {
			lineNumber++
			fields := configFields(s.Text())
			if len(fields) > 0 && (strings.EqualFold(fields[0], "Host") || strings.EqualFold(fields[0], "Match")) {
				flush()
				active, block = nil, nil
				start = lineNumber
			}
			if len(fields) < 2 {
				if len(active) > 0 {
					block = append(block, s.Text())
				}

				continue
			}
			switch strings.ToLower(fields[0]) {
			case "host":
				for _, host := range fields[1:] {
					if validHost(host) && !strings.ContainsAny(host, "*?!") {
						active = append(active, host)
						if !seenHosts[host] {
							seenHosts[host] = true
							hosts = append(hosts, host)
						}
					}
				}
			case "include":
				for _, pattern := range fields[1:] {
					if strings.HasPrefix(pattern, "~/") {
						pattern = filepath.Join(home, pattern[2:])
					} else if !filepath.IsAbs(pattern) {
						pattern = filepath.Join(home, ".ssh", pattern)
					}
					matches, err := filepath.Glob(pattern)
					if err != nil {
						return fmt.Errorf("Include %q: %w", pattern, err)
					}
					for _, match := range matches {
						if err := read(match); err != nil {
							return err
						}
					}
				}
			}
			if len(active) > 0 {
				block = append(block, s.Text())
			}
		}
		flush()
		return s.Err()
	}
	err := read(filepath.Join(home, ".ssh", "config"))
	sort.SliceStable(hosts, func(i, j int) bool {
		return strings.ToLower(hosts[i]) < strings.ToLower(hosts[j])
	})
	return hosts, previews, err
}

// configFields accepts quoted values, comments, and key=value syntax.
func configFields(line string) []string {
	var fields []string
	var word strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if word.Len() > 0 {
			fields = append(fields, word.String())
			word.Reset()
		}
	}
	for _, c := range line {
		if escaped {
			word.WriteRune(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		switch c {
		case '#':
			flush()
			return fields
		case '\'', '"':
			quote = c
		case ' ', '\t', '\r':
			flush()
		case '=':
			if len(fields) == 0 {
				flush()
			} else if len(fields) == 1 && word.Len() == 0 {
				// Optional whitespace around the keyword/value separator.
			} else {
				word.WriteRune(c)
			}
		default:
			word.WriteRune(c)
		}
	}
	flush()
	return fields
}

func serialDevices(root string) ([]string, error) {
	seen := map[string]bool{}
	var devices []string
	for _, pattern := range []string{"tty.usb*"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err == nil && info.Mode()&os.ModeCharDevice != 0 && !seen[path] {
				seen[path] = true
				devices = append(devices, path)
			}
		}
	}
	sort.Strings(devices)
	return devices, nil
}

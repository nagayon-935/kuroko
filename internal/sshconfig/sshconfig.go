// Package sshconfig lists the literal Host aliases defined in the user's
// OpenSSH client configuration (~/.ssh/config and the files it Includes),
// without evaluating Match exec or running ssh.
package sshconfig

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxSSHConfigFiles bounds total config files visited across Include globs.
	maxSSHConfigFiles = 128
	// maxSSHConfigBytes bounds bytes read per config file to prevent memory exhaustion.
	maxSSHConfigBytes = 1 << 20 // 1 MiB
)

// Hosts collects literal aliases, without evaluating Match exec or running SSH.
// Includes are scanned as candidate sources even inside conditional blocks;
// SSH itself remains responsible for evaluating the selected host's settings.
// Aliases that fail ValidHost (patterns, negations, option-like or
// non-printable names) are skipped. The result is sorted case-insensitively.
func Hosts(home string) ([]string, error) {
	hosts, _, err := Entries(home)
	return hosts, err
}

// Entries is Hosts plus, for each alias, a preview of the config block(s)
// that define it, each prefixed with "path:line".
func Entries(home string) ([]string, map[string]string, error) {
	r := &sshConfigReader{
		home:      home,
		previews:  map[string]string{},
		seenFiles: map[string]bool{},
		seenHosts: map[string]bool{},
	}
	err := r.readFile(filepath.Join(home, ".ssh", "config"))
	sort.SliceStable(r.hosts, func(i, j int) bool {
		return strings.ToLower(r.hosts[i]) < strings.ToLower(r.hosts[j])
	})
	return r.hosts, r.previews, err
}

type sshConfigReader struct {
	home      string
	hosts     []string
	previews  map[string]string
	seenFiles map[string]bool
	seenHosts map[string]bool
	fileCount int
}

func (r *sshConfigReader) markSeen(path string) bool {
	clean := filepath.Clean(path)
	if r.seenFiles[clean] {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		if r.seenFiles[resolved] {
			return false
		}
		r.seenFiles[resolved] = true
	}
	r.seenFiles[clean] = true
	return true
}

func (r *sshConfigReader) readFile(path string) error {
	if !r.markSeen(path) || r.fileCount >= maxSSHConfigFiles {
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Skip directories, FIFOs, sockets, and device nodes matched by Include globs.
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return err
	}
	r.fileCount++
	return r.scanFile(filepath.Clean(path), io.LimitReader(f, maxSSHConfigBytes))
}

func (r *sshConfigReader) scanFile(path string, reader io.Reader) error {
	s := bufio.NewScanner(reader)
	var active, block []string
	lineNumber, start := 0, 0
	for s.Scan() {
		lineNumber++
		text := s.Text()
		fields := configFields(text)
		if len(fields) > 0 && isBlockBoundary(fields[0]) {
			r.appendPreview(active, path, start, block)
			active, block = nil, nil
			start = lineNumber
		}
		if len(fields) >= 2 {
			switch strings.ToLower(fields[0]) {
			case "host":
				active = r.addHosts(fields[1:])
			case "include":
				if err := r.readIncludes(fields[1:]); err != nil {
					return err
				}
			}
		}
		if len(active) > 0 {
			block = append(block, text)
		}
	}
	r.appendPreview(active, path, start, block)
	return s.Err()
}

func isBlockBoundary(keyword string) bool {
	return strings.EqualFold(keyword, "Host") || strings.EqualFold(keyword, "Match")
}

func (r *sshConfigReader) addHosts(candidates []string) []string {
	var active []string
	for _, host := range candidates {
		if !ValidHost(host) {
			continue
		}
		active = append(active, host)
		if !r.seenHosts[host] {
			r.seenHosts[host] = true
			r.hosts = append(r.hosts, host)
		}
	}
	return active
}

func (r *sshConfigReader) appendPreview(active []string, path string, start int, block []string) {
	for _, host := range active {
		if r.previews[host] != "" {
			r.previews[host] += "\n\n"
		}
		r.previews[host] += fmt.Sprintf("%s:%d\n%s", path, start, strings.Join(block, "\n"))
	}
}

func (r *sshConfigReader) readIncludes(patterns []string) error {
	for _, pattern := range patterns {
		if pattern == "~" {
			continue
		}
		if strings.HasPrefix(pattern, "~/") {
			pattern = filepath.Join(r.home, pattern[2:])
		} else if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(r.home, ".ssh", pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("ssh config Include %q: %w", pattern, err)
		}
		for _, match := range matches {
			if err := r.readFile(match); err != nil {
				return err
			}
		}
	}
	return nil
}

// configFields accepts quoted values, comments, and key=value syntax.
func configFields(line string) []string {
	var sc fieldScanner
	for _, c := range line {
		if !sc.step(c) {
			break
		}
	}
	sc.flush()
	return sc.fields
}

type fieldScanner struct {
	fields  []string
	word    strings.Builder
	quote   rune
	escaped bool
}

func (s *fieldScanner) flush() {
	if s.word.Len() > 0 {
		s.fields = append(s.fields, s.word.String())
		s.word.Reset()
	}
}

func (s *fieldScanner) step(c rune) bool {
	if s.escaped {
		s.word.WriteRune(c)
		s.escaped = false
		return true
	}
	if c == '\\' {
		s.escaped = true
		return true
	}
	if s.quote != 0 {
		if c == s.quote {
			s.quote = 0
		} else {
			s.word.WriteRune(c)
		}
		return true
	}
	switch c {
	case '#':
		return false
	case '\'', '"':
		s.quote = c
	case ' ', '\t', '\r':
		s.flush()
	case '=':
		if len(s.fields) == 0 {
			s.flush()
		} else if len(s.fields) > 1 || s.word.Len() > 0 {
			s.word.WriteRune(c)
		}
	default:
		s.word.WriteRune(c)
	}
	return true
}

// ValidHost reports whether host is safe to pass to ssh as a destination:
// non-empty printable UTF-8 with no whitespace, no leading '-' (which ssh
// would parse as an option), and no ssh_config pattern characters. When a
// user@host prefix is present, both parts must be non-empty and the host
// part itself must also not start with '-'.
func ValidHost(host string) bool {
	if host == "" ||
		!utf8.ValidString(host) ||
		strings.HasPrefix(host, "-") ||
		strings.ContainsAny(host, "*?!") ||
		strings.ContainsFunc(host, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r) || !unicode.IsPrint(r)
		}) {
		return false
	}
	if idx := strings.LastIndex(host, "@"); idx >= 0 {
		user, h := host[:idx], host[idx+1:]
		if user == "" || h == "" || strings.HasPrefix(h, "-") {
			return false
		}
	}
	return true
}

// sshValueOptions are the ssh(1) single-letter options that take a value.
const sshValueOptions = "BbcDEeFIiJLlmOoPpQRSWw"

// OptionAwaitsValue reports whether the getopt cluster (dash stripped)
// ends in a value-taking ssh(1) option whose value is in the next argument.
func OptionAwaitsValue(cluster string) bool {
	for i, c := range cluster {
		if strings.ContainsRune(sshValueOptions, c) {
			return i == len(cluster)-1
		}
	}
	return false
}

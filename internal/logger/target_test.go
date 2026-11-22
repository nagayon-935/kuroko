package logger

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nagayon-935/kuroko/internal/logstore"
)

func TestExtractSSHTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"user@host", []string{"user@host"}, "user@host"},
		{"host only", []string{"hostname"}, "hostname"},
		{"-p flag", []string{"-p", "2222", "user@host"}, "user@host"},
		{"-i flag", []string{"-i", "key.pem", "user@host"}, "user@host"},
		{"-l and host", []string{"-l", "user", "hostname"}, "hostname"},
		{"multiple flags", []string{"-p", "22", "-i", "k", "host"}, "host"},
		{"empty", []string{}, ""},
		{"flags only", []string{"-v"}, ""},
		{"-J jump host then target", []string{"-J", "jump", "user@host"}, "user@host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSSHTarget(tt.args)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractSSHResolveOptions(t *testing.T) {
	limit := strings.Repeat("x", 4096)
	tests := []struct {
		name       string
		args, want []string
		ok         bool
	}{
		{"separated", []string{"-F", "work.conf", "-l", "user", "-p", "2222", "-J", "jump", "edge"}, []string{"-F", "work.conf", "-l", "user", "-p", "2222", "-J", "jump"}, true},
		{"attached and repeated", []string{"-Fwork.conf", "-oHostName=x", "-o", "Port=22", "edge"}, []string{"-F", "work.conf", "-o", "HostName=x", "-o", "Port=22"}, true},
		{"clusters", []string{"-v4F", "work.conf", "-A6p2222", "edge"}, []string{"-4", "-F", "work.conf", "-6", "-p", "2222"}, true},
		{"double dash", []string{"-6", "--", "-4"}, []string{"-6"}, true},
		{"after target", []string{"-4", "edge", "-o", "HostName=x"}, []string{"-4"}, true},
		{"unrelated", []string{"-v", "-A", "-t", "-i", "-4", "-viF6", "edge"}, nil, true},
		{"at limit", []string{"-F" + limit, "edge"}, []string{"-F", limit}, true},
		{"over limit", []string{"-F", limit + "x", "edge"}, nil, false},
		{"NUL", []string{"-oHostName=x\x00", "edge"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractSSHResolveOptions(tt.args)

			if ok != tt.ok || !slices.Equal(got, tt.want) {
				t.Errorf("got (%q, %t), want (%q, %t)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestTargetDetailsSSHOptions(t *testing.T) {
	dir := t.TempDir()
	const script = `#!/bin/sh
if [ "$#" -eq 5 ] && [ "$1" = -G ] && [ "$2" = -F ] && [ "$4" = -- ] && [ "$5" = edge ]; then
	case "$3" in
		work.conf) echo 'hostname work.example'; exit 0 ;;
		missing.conf) exit 1 ;;
	esac
fi
echo 'hostname default.example'
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"default config", []string{"ssh", "edge"}, "default.example"},
		{"work config", []string{"ssh", "-F", "work.conf", "edge"}, "work.example"},
		{"failed lookup", []string{"ssh", "-F", "missing.conf", "edge"}, "edge"},
		{"invalid option value", []string{"ssh", "-F", strings.Repeat("x", 4097), "edge"}, "edge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			address, hostname := TargetDetails(tt.args)

			if address != "edge" || hostname != tt.want {
				t.Errorf("got (%q, %q), want (%q, %q)", address, hostname, "edge", tt.want)
			}
		})
	}
}

func TestExtractScreenTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"full path", []string{"/dev/ttyUSB0"}, "ttyUSB0"},
		{"with baud rate", []string{"/dev/ttyUSB0", "115200"}, "ttyUSB0"},
		{"ttyS0", []string{"/dev/ttyS0"}, "ttyS0"},
		{"flag then device", []string{"-fn", "/dev/ttyUSB0"}, "ttyUSB0"},
		{"empty", []string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractScreenTarget(tt.args)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user@host", "user@host"},
		{"/dev/ttyUSB0", "_dev_ttyUSB0"},
		{"host:22", "host_22"},
		{"a b c", "a_b_c"},
		{"normal", "normal"},
		{"a/b\\c", "a_b_c"},
		{"a*b?c", "a_b_c"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitize(tt.input)
			if got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateFilename(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "ssh user@host",
			args:         []string{"ssh", "user@hostname"},
			wantContains: []string{"ssh", "hostname"},
			wantAbsent:   []string{"user@"},
		},
		{
			name:         "ssh with -p",
			args:         []string{"ssh", "-p", "2222", "user@host"},
			wantContains: []string{"ssh", "host"},
			wantAbsent:   []string{"user@"},
		},
		{
			name:         "screen device",
			args:         []string{"screen", "/dev/ttyUSB0", "115200"},
			wantContains: []string{"screen", "ttyUSB0"},
		},
		{
			name:         "empty args",
			args:         []string{},
			wantContains: []string{"unknown"},
		},
		{
			name:         "bash",
			args:         []string{"bash"},
			wantContains: []string{"bash"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateFilename(tt.args)
			if !strings.HasSuffix(got, ".log") {
				t.Errorf("filename %q has no .log suffix", got)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("filename %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("filename %q should not contain %q", got, absent)
				}
			}
		})
	}
}

// TestResolveSSHHostname verifies the fallback path (no real ssh -G call).
// We test that passing an already-canonical hostname returns it unchanged.
func TestResolveSSHHostnameFallback(t *testing.T) {
	// A host that is NOT an alias should come back as-is.
	// ssh -G will succeed but return the same hostname.
	got := resolveSSHHostname("localhost", nil)
	if got != "localhost" {
		t.Errorf("resolveSSHHostname(\"localhost\") = %q, want \"localhost\"", got)
	}
}

func TestResolveSSHHostnameEmpty(t *testing.T) {
	if got := resolveSSHHostname("", nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestExtractSSHTargetAndSanitize(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"ssh", "-vvp", "2222", "admin@router-01"}, "admin@router-01"},
		{[]string{"ssh", "-46p2222", "admin@router-01"}, "admin@router-01"},
		{[]string{"ssh", "-B", "eth0", "-O", "check", "-P", "tag", "router-02"}, "router-02"},
		{[]string{"ssh", "--", "router-03", "uptime"}, "router-03"},
	}
	for _, tt := range tests {
		if got := TargetAddress(tt.args); got != tt.want {
			t.Errorf("TargetAddress(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}

	// sanitize must strip control/format chars and cap byte length on a UTF-8 boundary.
	dirty := "host\x1b\u202e" + strings.Repeat("あ", 50)
	cleaned := sanitize(dirty)
	if strings.ContainsAny(cleaned, "\x1b\u202e") {
		t.Errorf("sanitize left control/format runes in %q", cleaned)
	}
	if len(cleaned) > maxFilenamePartBytes {
		t.Errorf("sanitize length = %d, want <= %d", len(cleaned), maxFilenamePartBytes)
	}
	if !utf8.ValidString(cleaned) {
		t.Errorf("sanitize produced invalid UTF-8: %q", cleaned)
	}
}

// logstore.RotateLogs only removes files whose names pass
// IsSessionLogName, so every name the writer can produce must pass it.
func TestGeneratedFilenamesAreSessionLogNames(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"bash"},
		{"/bin/bash"},
		{"ssh", "edgeSW03"},
		{"ssh", "-p", "2222", "admin@edgeSW03"},
		{"ssh", "user@10.0.0.1"},
		{"screen", "/dev/ttyUSB0", "115200"},
		{"python", "script.py"},
		{"ssh", "ルータ"},
		{"ssh", "host with space"},
		{"ssh", "a:b*c?d"},
	} {
		name := generateFilename(args)
		if !logstore.IsSessionLogName(name) {
			t.Errorf("generateFilename(%v) = %q; not accepted by logstore.IsSessionLogName", args, name)
		}
	}
}

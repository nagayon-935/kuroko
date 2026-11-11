package completion

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nagayon-935/kuroko/internal/config"
)

func writeSSHConfig(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestWrappedIndex(t *testing.T) {
	tests := []struct {
		name string
		ctx  []string
		want int
	}{
		{"nothing typed yet", nil, 0},
		{"builtin subcommand", []string{"view"}, 0},
		{"help is builtin", []string{"help"}, 0},
		{"wrapped command right after kuroko", []string{"ssh"}, 1},
		{"wrapped command with its own args", []string{"ssh", "-v"}, 1},
		{"wrapped command after global flags", []string{"-d", "/tmp/x", "ssh", "-v"}, 3},
		{"wrapped command after --log-dir = value split", []string{"--log-dir", "=", "/tmp/x", "telnet"}, 4},
		{"wrapped command after --", []string{"--", "ssh"}, 2},
		{"early-exit flag stops everything", []string{"-h", "ssh"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WrappedIndex(tt.ctx); got != tt.want {
				t.Fatalf("WrappedIndex(%q) = %d, want %d", tt.ctx, got, tt.want)
			}
		})
	}
}

func TestSSHDestinationCandidates(t *testing.T) {
	home := writeSSHConfig(t, "Host router edge *.lab\n  User admin\nHost !neg spine\n")
	hosts := []string{"edge", "router", "spine"}
	cfg := &config.Config{LogDir: t.TempDir()}

	tests := []struct {
		name string
		ctx  []string
		want []string
	}{
		{"destination right after ssh", []string{"ssh"}, hosts},
		{"after a boolean flag", []string{"ssh", "-v"}, hosts},
		{"value of -p is not a host", []string{"ssh", "-p"}, nil},
		{"after -p and its value", []string{"ssh", "-p", "22"}, hosts},
		{"value-taking option at end of a cluster", []string{"ssh", "-vp"}, nil},
		{"value attached to the option", []string{"ssh", "-p22"}, hosts},
		{"after -o option value", []string{"ssh", "-o", "StrictHostKeyChecking=no"}, hosts},
		{"host after user@ split by bash", []string{"ssh", "admin", "@"}, hosts},
		{"remote command after destination", []string{"ssh", "router"}, nil},
		{"after end of options", []string{"ssh", "-v", "--"}, hosts},
		{"ssh given as a path", []string{"/usr/bin/ssh"}, hosts},
		{"ssh after global flags", []string{"-d", "/tmp/x", "ssh"}, hosts},
		{"other wrapped commands get nothing", []string{"telnet"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Candidates(tt.ctx, cfg, home)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Candidates(%q) = %q, want %q", tt.ctx, got, tt.want)
			}
		})
	}
}

func TestSSHCandidatesWithoutHomeOrConfig(t *testing.T) {
	cfg := &config.Config{LogDir: t.TempDir()}
	for _, home := range []string{"", t.TempDir()} {
		if got := Candidates([]string{"ssh"}, cfg, home); len(got) != 0 {
			t.Fatalf("Candidates(ssh) with home %q = %q, want none", home, got)
		}
	}
}

func TestWrite(t *testing.T) {
	home := writeSSHConfig(t, "Host router\n")
	cfg := &config.Config{LogDir: t.TempDir()}

	tests := []struct {
		name string
		ctx  []string
		want string
	}{
		{"wrapped ssh prints the directive then hosts", []string{"-d", cfg.LogDir, "ssh"}, ":wrapped=3\nrouter\n"},
		{"wrapped command without fallback prints only the directive", []string{"telnet"}, ":wrapped=1\n"},
		{"builtin prints candidates without a directive", []string{"logs"}, "--info\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			if err := Write(&buf, tt.ctx, cfg, home); err != nil {
				t.Fatalf("Write error = %v", err)
			}
			if buf.String() != tt.want {
				t.Fatalf("Write(%q) = %q, want %q", tt.ctx, buf.String(), tt.want)
			}
		})
	}
}

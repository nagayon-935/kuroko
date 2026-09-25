package completion

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ryu/kuroko/internal/config"
)

func TestCandidates(t *testing.T) {
	logDir := t.TempDir()
	for _, f := range []string{"20260617_180000_ssh_edgeSW03.log", "20260101_000000_bash.log.gz"} {
		if err := os.WriteFile(filepath.Join(logDir, f), []byte("x"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	otherDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(otherDir, "20260101_010000_bash.log"), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := &config.Config{LogDir: logDir}

	tests := []struct {
		name string
		ctx  []string
		want []string
	}{
		{
			name: "empty context lists subcommands and global flags",
			ctx:  nil,
			want: []string{"logs", "view", "help", "completion",
				"--log-dir", "-d", "--help", "-h", "--version", "-v"},
		},
		{
			name: "view lists log files from configured log dir",
			ctx:  []string{"view"},
			want: []string{"20260101_000000_bash.log.gz", "20260617_180000_ssh_edgeSW03.log"},
		},
		{
			name: "logs subcommand offers nothing (flags after it are ignored)",
			ctx:  []string{"logs"},
			want: nil,
		},
		{
			name: "help subcommand offers nothing",
			ctx:  []string{"help"},
			want: nil,
		},
		{
			name: "view takes a single file: nothing after its argument",
			ctx:  []string{"view", "-d", otherDir},
			want: nil,
		},
		{
			name: "completion takes a single shell: nothing after it",
			ctx:  []string{"completion", "bash"},
			want: nil,
		},
		{
			name: "--log-dir=DIR single token redirects view completion",
			ctx:  []string{"--log-dir=" + otherDir, "view"},
			want: []string{"20260101_010000_bash.log"},
		},
		{
			name: "--log-dir = DIR split by COMP_WORDBREAKS redirects view completion",
			ctx:  []string{"--log-dir", "=", otherDir, "view"},
			want: []string{"20260101_010000_bash.log"},
		},
		{
			name: "single-dash -log-dir form redirects view completion",
			ctx:  []string{"-log-dir", otherDir, "view"},
			want: []string{"20260101_010000_bash.log"},
		},
		{
			name: "help flag short-circuits: main prints usage and exits",
			ctx:  []string{"-h", "view"},
			want: nil,
		},
		{
			name: "version flag short-circuits",
			ctx:  []string{"--version", "view"},
			want: nil,
		},
		{
			name: "help flag explicitly set false does not short-circuit",
			ctx:  []string{"--help=false", "view"},
			want: []string{"20260101_000000_bash.log.gz", "20260617_180000_ssh_edgeSW03.log"},
		},
		{
			name: "empty log-dir value falls back to configured dir (like config.Load)",
			ctx:  []string{"--log-dir=", "view"},
			want: []string{"20260101_000000_bash.log.gz", "20260617_180000_ssh_edgeSW03.log"},
		},
		{
			name: "last log-dir wins, and an empty last value means configured dir",
			ctx:  []string{"-d", otherDir, "-d=", "view"},
			want: []string{"20260101_000000_bash.log.gz", "20260617_180000_ssh_edgeSW03.log"},
		},
		{
			name: "last non-empty log-dir wins",
			ctx:  []string{"-d", "/nonexistent", "-d", otherDir, "view"},
			want: []string{"20260101_010000_bash.log"},
		},
		{
			name: "double dash terminates flags",
			ctx:  []string{"--", "view"},
			want: []string{"20260101_000000_bash.log.gz", "20260617_180000_ssh_edgeSW03.log"},
		},
		{
			name: "completion subcommand lists supported shells",
			ctx:  []string{"completion"},
			want: []string{"bash", "zsh", "fish"},
		},
		{
			name: "log-dir override redirects view completion",
			ctx:  []string{"-d", otherDir, "view"},
			want: []string{"20260101_010000_bash.log"},
		},
		{
			name: "unknown leading word (wrapped command) yields no candidates",
			ctx:  []string{"ssh"},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Candidates(tt.ctx, cfg)
			sortedGot := append([]string(nil), got...)
			sortedWant := append([]string(nil), tt.want...)
			sort.Strings(sortedGot)
			sort.Strings(sortedWant)
			if !reflect.DeepEqual(sortedGot, sortedWant) {
				t.Fatalf("Candidates(%v) = %v, want %v", tt.ctx, got, tt.want)
			}
		})
	}
}

func TestWriteScript(t *testing.T) {
	t.Run("bash returns a completion function sourced by name", func(t *testing.T) {
		var buf strings.Builder
		if err := WriteScript(&buf, "bash"); err != nil {
			t.Fatalf("WriteScript(bash) error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "_kuroko") || !strings.Contains(out, "complete -F _kuroko kuroko") {
			t.Fatalf("bash script missing expected registration, got:\n%s", out)
		}
	})

	t.Run("empty shell defaults to bash", func(t *testing.T) {
		var buf strings.Builder
		if err := WriteScript(&buf, ""); err != nil {
			t.Fatalf("WriteScript(\"\") error = %v", err)
		}
		if !strings.Contains(buf.String(), "_kuroko") {
			t.Fatalf("expected bash script for empty shell, got:\n%s", buf.String())
		}
	})

	for _, shell := range []string{"zsh", "fish"} {
		t.Run(shell+" is not yet supported", func(t *testing.T) {
			var buf strings.Builder
			if err := WriteScript(&buf, shell); err == nil {
				t.Fatalf("WriteScript(%s) expected error, got nil", shell)
			}
		})
	}
}

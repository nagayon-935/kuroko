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
			name: "logs subcommand offers only global flags",
			ctx:  []string{"logs"},
			want: []string{"--log-dir", "-d", "--help", "-h", "--version", "-v"},
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

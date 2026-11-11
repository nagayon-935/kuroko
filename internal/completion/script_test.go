package completion

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runBashScript sources the bash completion script with `kuroko` stubbed
// to print stub, optionally runs setup, completes words (the last one is
// the word under the cursor), and returns COMPREPLY one entry per line.
func runBashScript(t *testing.T, stub, setup string, words ...string) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "kuroko.bash")
	if err := os.WriteFile(scriptPath, []byte(bashScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zzfile"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	driver := `
. "$1"; shift
kuroko() { printf '%s' "$STUB"; }
eval "$SETUP"
COMP_WORDS=("$@"); COMP_CWORD=$(( $# - 1 ))
_kuroko
printf '%s\n' "${COMPREPLY[@]}"
`
	cmd := exec.Command(bash, append([]string{"--norc", "--noprofile", "-c", driver, "bash", scriptPath}, words...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "STUB="+stub, "SETUP="+setup)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("completing ran a command embedded in a candidate")
	}
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == '\n' })
}

func TestBashScript(t *testing.T) {
	const hosts = ":wrapped=1\nrouter\nedge\n"
	tests := []struct {
		name  string
		stub  string
		setup string
		words []string
		want  []string
	}{
		{
			name:  "builtin candidates are filtered by the current word",
			stub:  "--last\nview-a.log\n",
			words: []string{"kuroko", "view", "--"},
			want:  []string{"--last"},
		},
		{
			name:  "wrapped fallback filters hosts",
			stub:  hosts,
			words: []string{"kuroko", "ssh", "r"},
			want:  []string{"router"},
		},
		{
			name:  "user@ with @ as a word break replies from the @ on",
			stub:  hosts,
			setup: `COMP_WORDBREAKS=$' \t\n"\'@><=;|&(:'`,
			words: []string{"kuroko", "ssh", "admin@r"},
			want:  []string{"@router"},
		},
		{
			name:  "bash 4+ split user @ ho replies from the @ on",
			stub:  hosts,
			setup: `COMP_WORDBREAKS=$' \t\n"\'@><=;|&(:'`,
			words: []string{"kuroko", "ssh", "admin", "@", "r"},
			want:  []string{"@router"},
		},
		{
			name: "bash 4+ right after user@ asks for hosts after the @",
			// Only answer when the lone "@" is passed on as context.
			setup: `COMP_WORDBREAKS=$' \t\n"\'@><=;|&(:'
kuroko() { [ "${@: -1}" = "@" ] && printf ':wrapped=1\nrouter\nedge\n'; }`,
			words: []string{"kuroko", "ssh", "admin", "@"},
			want:  []string{"@router", "@edge"},
		},
		{
			name:  "user@ without @ as a word break keeps the user",
			stub:  hosts,
			setup: `COMP_WORDBREAKS=$' \t\n"\'><=;|&(:'`,
			words: []string{"kuroko", "ssh", "admin@r"},
			want:  []string{"admin@router"},
		},
		{
			name:  "wrapped command without candidates falls back to files",
			stub:  ":wrapped=1\n",
			words: []string{"kuroko", "telnet", "zz"},
			want:  []string{"zzfile"},
		},
		{
			name:  "bash-completion's _command_offset takes over when loaded",
			stub:  ":wrapped=3\nrouter\n",
			setup: `_command_offset() { COMPREPLY=("delegated-$1"); }`,
			words: []string{"kuroko", "-d", "/tmp", "ssh", "r"},
			want:  []string{"delegated-3"},
		},
		{
			name: "newer _comp_command_offset is preferred",
			stub: hosts,
			setup: `_comp_command_offset() { COMPREPLY=("new-$1"); }
_command_offset() { COMPREPLY=("old-$1"); }`,
			words: []string{"kuroko", "ssh", ""},
			want:  []string{"new-1"},
		},
		{
			name:  "ssh hosts are matched literally, never expanded",
			stub:  ":wrapped=1\nx$(touch pwned)\nx$HOME\n",
			words: []string{"kuroko", "ssh", "x"},
			want:  []string{"x$(touch pwned)", "x$HOME"},
		},
		{
			name:  "log names are matched literally, never expanded",
			stub:  "--last\n`touch pwned`.log\n",
			words: []string{"kuroko", "view", "`"},
			want:  []string{"`touch pwned`.log"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runBashScript(t, tt.stub, tt.setup, tt.words...)
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Fatalf("COMPREPLY = %q, want %q", got, tt.want)
			}
		})
	}
}

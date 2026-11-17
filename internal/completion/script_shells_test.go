package completion

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeKuroko installs a `kuroko` stand-in in a fresh directory and
// returns that directory (to be put first on PATH) and the file the stub
// records its arguments in. The stub prints $KUROKO_STUB, or fails noisily
// when $KUROKO_FAIL is set.
func writeFakeKuroko(t *testing.T) (binDir, argsFile string) {
	t.Helper()
	binDir = t.TempDir()
	argsFile = filepath.Join(binDir, "args")
	stub := `#!/bin/sh
printf '%s\n' "$@" > "$KUROKO_ARGS"
if [ -n "$KUROKO_FAIL" ]; then echo "boom" >&2; exit 1; fi
printf '%s' "$KUROKO_STUB"
`
	if err := os.WriteFile(filepath.Join(binDir, "kuroko"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	return binDir, argsFile
}

// shellEnv returns an environment where the fake kuroko comes first on PATH.
func shellEnv(binDir, argsFile, stub, home string, extra ...string) []string {
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"KUROKO_ARGS=" + argsFile,
		"KUROKO_STUB=" + stub,
		"TERM=dumb",
	}
	return append(env, extra...)
}

func writeScriptFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireShell(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not installed", name)
	}
	return path
}

// zshDriver runs the completion function outside a real completion widget:
// compadd and the zsh helpers it calls are replaced by printers, so the test
// sees exactly what the script asks zsh to do. $1 = script, $2 = CURRENT,
// the rest = words.
const zshDriver = `
autoload -Uz compinit; compinit -u -D
source "$1"; shift
print -r -- "registered=$_comps[kuroko]"
compadd() { shift; print -r -- "compadd $*" }
_normal() { print -r -- "normal CURRENT=$CURRENT words=$words" }
_files() { print -r -- "files $*" }
_command_names() { print -r -- "cmdnames $*" }
CURRENT=$1; shift; words=("$@")
_kuroko 2>&1; print -r -- "rc=$?"
`

func runZshDriver(t *testing.T, stub string, extraEnv []string, current string, words ...string) (out, args string) {
	t.Helper()
	zsh := requireShell(t, "zsh")
	binDir, argsFile := writeFakeKuroko(t)
	script := writeScriptFile(t, t.TempDir(), "kuroko.zsh", zshScript)
	argv := append([]string{"-f", "-c", zshDriver, "zsh", script, current}, words...)
	cmd := exec.Command(zsh, argv...)
	cmd.Env = shellEnv(binDir, argsFile, stub, t.TempDir(), extraEnv...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh: %v\n%s", err, b)
	}
	recorded, _ := os.ReadFile(argsFile)
	return string(b), string(recorded)
}

func TestZshScriptSyntax(t *testing.T) {
	zsh := requireShell(t, "zsh")
	script := writeScriptFile(t, t.TempDir(), "kuroko.zsh", zshScript)
	if b, err := exec.Command(zsh, "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("zsh -n: %v\n%s", err, b)
	}
}

func TestZshScriptRegistration(t *testing.T) {
	zsh := requireShell(t, "zsh")

	t.Run("eval after compinit registers _kuroko for kuroko", func(t *testing.T) {
		script := writeScriptFile(t, t.TempDir(), "kuroko.zsh", zshScript)
		driver := `autoload -Uz compinit; compinit -u -D
eval "$(cat "$1")"
print -r -- "comp=$_comps[kuroko] defined=$+functions[_kuroko]"`
		cmd := exec.Command(zsh, "-f", "-c", driver, "zsh", script)
		cmd.Env = shellEnv(t.TempDir(), "/dev/null", "", t.TempDir())
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("zsh: %v\n%s", err, b)
		}
		if got := strings.TrimSpace(string(b)); got != "comp=_kuroko defined=1" {
			t.Fatalf("got %q, want comp=_kuroko defined=1", got)
		}
	})

	t.Run("file named _kuroko on fpath is picked up by compinit", func(t *testing.T) {
		fpathDir := t.TempDir()
		writeScriptFile(t, fpathDir, "_kuroko", zshScript)
		driver := `fpath=("$1" $fpath)
autoload -Uz compinit; compinit -u -D
print -r -- "comp=$_comps[kuroko]"`
		cmd := exec.Command(zsh, "-f", "-c", driver, "zsh", fpathDir)
		cmd.Env = shellEnv(t.TempDir(), "/dev/null", "", t.TempDir())
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("zsh: %v\n%s", err, b)
		}
		if got := strings.TrimSpace(string(b)); got != "comp=_kuroko" {
			t.Fatalf("got %q, want comp=_kuroko", got)
		}
	})
}

func TestZshScriptCompletion(t *testing.T) {
	tests := []struct {
		name     string
		stub     string
		env      []string
		current  string
		words    []string
		want     []string // lines that must appear
		wantNot  []string // substrings that must not appear
		wantArgs string
	}{
		{
			name: "subcommand candidates are added and commands offered after kuroko", stub: "view\nlogs\n",
			current: "2", words: []string{"kuroko", "v"},
			want: []string{"registered=_kuroko", "compadd view logs", "cmdnames -e", "rc=0"}, wantArgs: "__complete\n",
		},
		{
			name: "only words before the cursor are passed to the helper", stub: "--last\n",
			current: "3", words: []string{"kuroko", "view", "--l"},
			want: []string{"compadd --last", "rc=0"}, wantNot: []string{"cmdnames"}, wantArgs: "__complete\nview\n",
		},
		{
			name: "wrapped command delegates to _normal with shifted words", stub: ":wrapped=1\nrouter\n",
			current: "3", words: []string{"kuroko", "ssh", ""},
			want: []string{"normal CURRENT=2 words=ssh "}, wantNot: []string{"compadd", "router"}, wantArgs: "__complete\nssh\n",
		},
		{
			name: "wrapped command after global flags skips them", stub: ":wrapped=3\n",
			current: "6", words: []string{"kuroko", "-d", "/tmp", "ssh", "-v", ""},
			want: []string{"normal CURRENT=3 words=ssh -v "}, wantArgs: "__complete\n-d\n/tmp\nssh\n-v\n",
		},
		{
			name: "value of -d is completed as a directory", stub: "view\nlogs\n",
			current: "3", words: []string{"kuroko", "-d", ""},
			want: []string{"files -/"}, wantNot: []string{"compadd"},
		},
		{
			name: "value of --log-dir is completed as a directory", stub: "view\n",
			current: "3", words: []string{"kuroko", "--log-dir", ""},
			want: []string{"files -/"}, wantNot: []string{"compadd"},
		},
		{
			name: "a wrapped command's own -d is not a log dir", stub: ":wrapped=1\n",
			current: "4", words: []string{"kuroko", "tool", "-d", ""},
			want: []string{"normal CURRENT=3 words=tool -d "}, wantNot: []string{"files"},
		},
		{
			name: "candidates are never evaluated", stub: "$(touch pwned)\n`touch pwned`\n",
			current: "2", words: []string{"kuroko", ""},
			want: []string{"compadd $(touch pwned) `touch pwned`"},
		},
		{
			name: "a failing kuroko prints nothing and adds no candidates", stub: "", env: []string{"KUROKO_FAIL=1"},
			current: "3", words: []string{"kuroko", "view", ""},
			want: []string{"rc=0"}, wantNot: []string{"boom", "compadd"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, args := runZshDriver(t, tt.stub, tt.env, tt.current, tt.words...)
			lines := strings.Split(out, "\n")
			for _, w := range tt.want {
				found := false
				for _, l := range lines {
					if l == w {
						found = true
					}
				}
				if !found {
					t.Errorf("output missing line %q:\n%s", w, out)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(out, w) {
					t.Errorf("output must not contain %q:\n%s", w, out)
				}
			}
			if tt.wantArgs != "" && args != tt.wantArgs {
				t.Errorf("kuroko args = %q, want %q", args, tt.wantArgs)
			}
		})
	}
}

func TestFishScriptSyntax(t *testing.T) {
	fish := requireShell(t, "fish")
	script := writeScriptFile(t, t.TempDir(), "kuroko.fish", fishScript)
	if b, err := exec.Command(fish, "--no-config", "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("fish -n: %v\n%s", err, b)
	}
}

// runFishComplete sources the fish script and returns the candidates fish
// offers for line (what `complete -C` prints), run in dir.
func runFishComplete(t *testing.T, stub, line, dir, home string, extraEnv ...string) (out, args string) {
	t.Helper()
	fish := requireShell(t, "fish")
	binDir, argsFile := writeFakeKuroko(t)
	script := writeScriptFile(t, t.TempDir(), "kuroko.fish", fishScript)
	cmd := exec.Command(fish, "--no-config", "-c", `source $argv[1]; complete -C $argv[2]`, script, line)
	cmd.Dir = dir
	cmd.Env = shellEnv(binDir, argsFile, stub, home, extraEnv...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish: %v\n%s", err, b)
	}
	recorded, _ := os.ReadFile(argsFile)
	return string(b), string(recorded)
}

// fishCandidates returns the candidate of each `complete -C` output line (the
// text before the optional tab-separated description).
func fishCandidates(out string) []string {
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		got = append(got, strings.SplitN(l, "\t", 2)[0])
	}
	return got
}

func TestFishScriptCompletion(t *testing.T) {
	contains := func(got []string, want string) bool {
		for _, g := range got {
			if g == want {
				return true
			}
		}
		return false
	}

	t.Run("subcommand candidates come from kuroko __complete", func(t *testing.T) {
		out, args := runFishComplete(t, "view\nlogs\n", "kuroko v", t.TempDir(), t.TempDir())
		got := fishCandidates(out)
		if !contains(got, "view") || contains(got, "logs") {
			t.Fatalf("candidates = %q, want view but not logs", got)
		}
		if args != "__complete\n" {
			t.Fatalf("kuroko args = %q", args)
		}
	})

	t.Run("words before the cursor are passed to the helper", func(t *testing.T) {
		out, args := runFishComplete(t, "--last\nx.log\n", "kuroko view --l", t.TempDir(), t.TempDir())
		if got := fishCandidates(out); !contains(got, "--last") {
			t.Fatalf("candidates = %q, want --last", got)
		}
		if args != "__complete\nview\n" {
			t.Fatalf("kuroko args = %q", args)
		}
	})

	t.Run("wrapped command is completed by fish itself", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeScriptFile(t, filepath.Join(home, ".ssh"), "config", "Host zzrouter\n  User admin\n")
		out, args := runFishComplete(t, ":wrapped=1\n", "kuroko ssh zzr", t.TempDir(), home)
		if got := fishCandidates(out); !contains(got, "zzrouter") {
			t.Fatalf("candidates = %q, want ssh host zzrouter", got)
		}
		if args != "__complete\nssh\n" {
			t.Fatalf("kuroko args = %q", args)
		}
	})

	t.Run("value of -d is completed as a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeScriptFile(t, dir, "afile", "")
		out, _ := runFishComplete(t, "view\n", "kuroko -d s", dir, t.TempDir())
		got := fishCandidates(out)
		if !contains(got, "subdir/") || contains(got, "view") {
			t.Fatalf("candidates = %q, want subdir/ only", got)
		}
	})

	t.Run("only kuroko typed with an empty current token offers the helper's candidates", func(t *testing.T) {
		out, args := runFishComplete(t, "view\nlogs\n", "kuroko ", t.TempDir(), t.TempDir())
		got := fishCandidates(out)
		if !contains(got, "view") || !contains(got, "logs") {
			t.Fatalf("candidates = %q, want view and logs", got)
		}
		if args != "__complete\n" {
			t.Fatalf("kuroko args = %q", args)
		}
	})

	t.Run("a helper that prints nothing yields no candidates", func(t *testing.T) {
		out, args := runFishComplete(t, "", "kuroko ", t.TempDir(), t.TempDir())
		if got := fishCandidates(out); len(got) != 0 {
			t.Fatalf("candidates = %q, want none", got)
		}
		if strings.TrimSpace(out) != "" {
			t.Fatalf("output = %q, want empty (no empty candidate)", out)
		}
		if args != "__complete\n" {
			t.Fatalf("kuroko args = %q", args)
		}
	})

	t.Run("a failing kuroko yields no kuroko candidates and no error text", func(t *testing.T) {
		out, _ := runFishComplete(t, "", "kuroko view ", t.TempDir(), t.TempDir(), "KUROKO_FAIL=1")
		if strings.Contains(out, "boom") {
			t.Fatalf("stderr leaked into completion output: %q", out)
		}
	})
}

package logger

import (
	"bytes"
	"math/rand"
	"testing"
	"time"
)

// splitPromptReference is the straightforward definition SplitPrompt must
// match: the shortest prefix (within maxPromptLen) accepted by IsShellPrompt.
func splitPromptReference(line []byte) ([]byte, []byte) {
	for i := 1; i <= min(len(line), maxPromptLen); i++ {
		if IsShellPrompt(line[:i]) {
			return line[:i], line[i:]
		}
	}
	return nil, nil
}

func assertSplitPromptMatchesReference(t *testing.T, line []byte) {
	t.Helper()
	gotPrompt, gotCmd := SplitPrompt(line)
	wantPrompt, wantCmd := splitPromptReference(line)
	if !bytes.Equal(gotPrompt, wantPrompt) || !bytes.Equal(gotCmd, wantCmd) ||
		(gotPrompt == nil) != (wantPrompt == nil) {
		t.Fatalf("SplitPrompt(%q) = (%q, %q), want (%q, %q)",
			line, gotPrompt, gotCmd, wantPrompt, wantCmd)
	}
}

var splitPromptSeeds = []string{
	"user@host:~$ ls -la",
	"[user@host ~]$ cat foo",
	"root@server:/etc# vim hosts",
	"user@mac ~ % ls",
	"~/src ❯ git status",
	"projects ➔ make",
	"➔ ❯ mixed",
	"❯ ➔ mixed",
	"(venv) user@host:~/proj$ python",
	"just some output",
	"echo $ hello",
	"a > b > c",
	"$ $ $ $",
	"# comment $ ",
	"   indented$ ",
	"user@host:~$ echo 'a$ b# c'",
	"\x00$ ",
	"user @host$ ",
}

func TestSplitPromptMatchesReference(t *testing.T) {
	for _, seed := range splitPromptSeeds {
		assertSplitPromptMatchesReference(t, []byte(seed))
	}

	// Random lines built from prompt-relevant fragments, so the indicator,
	// arrow and validation branches all get exercised in many orders.
	fragments := []string{
		"a", "Z", "0", "_", "-", ".", "@", ":", "~", "/", "[", "]", "(", ")",
		"+", " ", "\t", "$", "#", "%", ">", "$ ", "# ", "% ", "> ", "❯", "➔",
		"❯ ", "➔ ", "!", "'", " ", "\xe2", "\x9d",
	}
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 20000; n++ {
		var b bytes.Buffer
		for k := rng.Intn(24); k > 0; k-- {
			b.WriteString(fragments[rng.Intn(len(fragments))])
		}
		assertSplitPromptMatchesReference(t, b.Bytes())
	}
}

func TestSplitPromptIgnoresIndicatorBeyondMaxPromptLen(t *testing.T) {
	line := append(bytes.Repeat([]byte("a"), maxPromptLen), []byte("@host:~$ ls")...)
	if prompt, _ := SplitPrompt(line); prompt != nil {
		t.Fatalf("SplitPrompt matched a %d-byte prompt beyond maxPromptLen", len(prompt))
	}
}

// TestSplitPromptLongLineIsFast guards against the quadratic scan that let a
// single long output line stall the terminal for seconds.
func TestSplitPromptLongLineIsFast(t *testing.T) {
	lines := [][]byte{
		bytes.Repeat([]byte("a"), 1<<20),
		bytes.Repeat([]byte("ab/cd "), 1<<18),
		bytes.Repeat([]byte("> "), 1<<19),
	}
	start := time.Now()
	for _, line := range lines {
		SplitPrompt(line)
	}
	// The quadratic implementation took tens of seconds here; the linear
	// one finishes in well under a millisecond. The bound is loose so slow
	// CI runners never flake.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("SplitPrompt on long lines took %v", elapsed)
	}
}

func FuzzSplitPrompt(f *testing.F) {
	for _, seed := range splitPromptSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		assertSplitPromptMatchesReference(t, line)
	})
}

func BenchmarkSplitPromptLongLine(b *testing.B) {
	line := bytes.Repeat([]byte("a"), 100_000)
	for i := 0; i < b.N; i++ {
		SplitPrompt(line)
	}
}

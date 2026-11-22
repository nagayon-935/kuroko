package viewer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nagayon-935/kuroko/internal/logger"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

func byteLines(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// outputText joins the viewer's output lines with LF.
func outputText(v *Viewer) string {
	return string(bytes.Join(v.currentOutputLines, []byte("\n")))
}

// ---- Oracles: verbatim copies of the previous implementations ----

func oldParseMetadata(logData []byte) []CommandMetadata {
	var cmds []CommandMetadata
	var currentOffset int64 = 0

	lines := bytes.Split(logData, []byte("\n"))
	for i, line := range lines {
		lineLen := int64(len(line)) + 1

		lineStr := strings.TrimSuffix(string(line), "\r")

		prefix := "# kuroko:cmd:"
		if strings.HasPrefix(lineStr, prefix) {
			payload := lineStr[len(prefix):]

			var timestamp string
			var command string

			if idx := strings.Index(payload, "|"); idx >= 0 {
				timestamp = payload[:idx]
				command = payload[idx+1:]
			} else {
				timestamp = payload
				if i+1 < len(lines) {
					_, cmdBytes, _ := logger.SplitPromptInfo(lines[i+1])
					if len(cmdBytes) > 0 {
						command = string(cmdBytes)
					} else {
						command = string(lines[i+1])
					}
				}
			}

			command = strings.TrimSpace(strings.ReplaceAll(command, "\r", ""))

			cmds = append(cmds, CommandMetadata{
				Timestamp: timestamp,
				Command:   command,
				Offset:    currentOffset,
			})
		}
		currentOffset += lineLen
	}
	return cmds
}

// oldOutputLines is the previous line building: writeFiltered, then Split.
func oldOutputLines(t testing.TB, data []byte) []string {
	var out bytes.Buffer
	if err := writeFiltered(bytes.NewReader(data), &out); err != nil {
		t.Fatalf("writeFiltered: %v", err)
	}
	return strings.Split(out.String(), "\n")
}

func oldMatches(lines []string, query string) []int {
	var m []int
	if query == "" {
		return nil
	}
	q := strings.ToLower(query)
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), q) {
			m = append(m, i)
		}
	}
	return m
}

// ---- Differential helpers ----

func checkParse(t testing.TB, data []byte) {
	t.Helper()
	v := &Viewer{logData: data}
	v.parseMetadata()
	want := oldParseMetadata(data)
	if len(v.allCmds) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(v.allCmds, want) {
		t.Fatalf("parseMetadata mismatch for %q:\n got %+v\nwant %+v", trunc(data), v.allCmds, want)
	}
}

func checkLines(t testing.TB, data []byte) {
	t.Helper()
	got := splitOutputLines(data)
	want := oldOutputLines(t, data)
	if len(got) != len(want) {
		t.Fatalf("line count %d != %d for %q", len(got), len(want), trunc(data))
	}
	for i := range got {
		if string(got[i]) != want[i] {
			t.Fatalf("line %d differs for %q: %q vs %q", i, trunc(data), trunc(got[i]), trunc([]byte(want[i])))
		}
	}
}

func checkMatches(t testing.TB, data []byte, query string) {
	t.Helper()
	v := &Viewer{currentOutputLines: splitOutputLines(data), outputQuery: query}
	v.updateMatches()
	want := oldMatches(oldOutputLines(t, data), query)
	if !reflect.DeepEqual(v.matchLines, want) && (len(v.matchLines) != 0 || len(want) != 0) {
		t.Fatalf("matches for query %q: got %v want %v", query, v.matchLines, want)
	}
}

func trunc(b []byte) string {
	if len(b) > 80 {
		return string(b[:80]) + "..."
	}
	return string(b)
}

const meta = "# kuroko:cmd:2026-06-18T12:00:01+09:00\n"

func equivCorpus() map[string][]byte {
	long64k := strings.Repeat("x", 70*1024)
	long4k := strings.Repeat("y", 5000)
	return map[string][]byte{
		"fixture":           []byte(testLogContent),
		"empty":             {},
		"only-lf":           []byte("\n"),
		"no-commands":       []byte("login banner\nfoo\n"),
		"no-trailing-lf":    []byte(meta + "user@host:~$ ls\nfile1"),
		"meta-last-no-lf":   []byte("a\n# kuroko:cmd:2026-06-18T12:00:01+09:00"),
		"meta-last-lf":      []byte("a\n" + meta),
		"meta-only":         []byte("# kuroko:cmd:x"),
		"meta-only-lf":      []byte(meta),
		"meta-twice":        []byte(meta + meta + "x\n"),
		"old-format":        []byte("# kuroko:cmd:2026-06-18T12:00:01+09:00|ls -la\nout\n# kuroko:cmd:ts|\nz\n"),
		"old-format-cr":     []byte("# kuroko:cmd:2026|show ver\r\nout\r\n"),
		"crlf":              []byte("# kuroko:cmd:2026-06-18T12:00:01+09:00\r\nuser@host:~$ ls\r\nfile\r\n# kuroko:cmd:t2\r\nRouter#show ip\r\nx\r\n"),
		"next-cr-untrimmed": []byte("# kuroko:cmd:t\nplain command\r\n"),
		"next-empty":        []byte("# kuroko:cmd:t\n\nfoo\n"),
		"long-64k":          []byte("a\n" + long64k + "\n" + meta + long64k),
		"long-meta-prefix":  []byte("# kuroko:cmd:" + long4k + "\nafter\n# kuroko:cmd:2\n" + long4k + "\n"),
		"exact-4096":        []byte(strings.Repeat("z", 4096) + "\nq\n"),
		"exact-4096-eof":    []byte(strings.Repeat("z", 4096)),
		"exact-4095":        []byte(strings.Repeat("z", 4095) + "\n"),
		"exact-4097-meta":   []byte("# kuroko:cmd:" + strings.Repeat("m", 4096-13) + "\nk\n"),
		"short-prefix":      []byte("# kuroko:cm\n# kuroko:cmd\n# kuroko:cmd:\nok\n"),
		"unicode": []byte(meta + "İstanbul\nStraße GROSS\nGigabitEthernet0/1 is up, LINE protocol is up\n" +
			"\u212Aelvin\nfoo\xff\xfebar\n"),
	}
}

var equivQueries = []string{
	"", "a", "A", "ls", "LS", "İ", "i", "ß", "ss", "SS", "k", "K", "\u212A", "kelvin",
	"GigabitEthernet", "LINE protocol", "line PROTOCOL", "zzz", "\xff", "x", "é", "İstanbul",
}

func TestEquivalenceParseMetadata(t *testing.T) {
	for name, data := range equivCorpus() {
		t.Run(name, func(t *testing.T) { checkParse(t, data) })
	}
}

func TestEquivalenceOutputLines(t *testing.T) {
	for name, data := range equivCorpus() {
		t.Run(name, func(t *testing.T) { checkLines(t, data) })
	}
}

func TestEquivalenceMatches(t *testing.T) {
	for name, data := range equivCorpus() {
		t.Run(name, func(t *testing.T) {
			for _, q := range equivQueries {
				checkMatches(t, data, q)
			}
		})
	}
}

func TestEquivalenceCommandRanges(t *testing.T) {
	for name, data := range equivCorpus() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "x.log")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			v, err := newViewer(path)
			if err != nil {
				t.Fatal(err)
			}
			for i := range v.allCmds {
				rng, err := v.commandRange(i)
				if err != nil {
					continue
				}
				checkLines(t, rng)
			}
		})
	}
}

func TestLoadSessionReadsExactBytes(t *testing.T) {
	data := equivCorpus()["fixture"]
	path := filepath.Join(t.TempDir(), "x.log")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := loadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(v.logData, data) {
		t.Fatal("logData differs from file content")
	}
	// Empty file.
	empty := filepath.Join(t.TempDir(), "e.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err = loadSession(empty); err != nil || len(v.logData) != 0 {
		t.Fatalf("empty: %v %d", err, len(v.logData))
	}
}

func TestLoadSessionGzipUnchanged(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "a.log")
	if err := os.WriteFile(plain, []byte(testLogContent), 0o600); err != nil {
		t.Fatal(err)
	}
	gz := compressForTest(t, plain)
	v, err := loadSession(gz)
	if err != nil {
		t.Fatal(err)
	}
	if string(v.logData) != testLogContent {
		t.Fatal("gz content mismatch")
	}
}

func FuzzOutputLinesEquivalence(f *testing.F) {
	for _, d := range equivCorpus() {
		f.Add(d)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		checkLines(t, data)
	})
}

func FuzzParseMetadataEquivalence(f *testing.F) {
	for _, d := range equivCorpus() {
		f.Add(d)
	}
	f.Add([]byte("# kuroko:cmd:t\r\n# kuroko:cmd:u|v\r"))
	f.Fuzz(func(t *testing.T, data []byte) {
		checkParse(t, data)
	})
}

func FuzzMatchesEquivalence(f *testing.F) {
	f.Add([]byte("Hello WORLD\nİstanbul\n"), "hello")
	f.Add([]byte("\u212Aelvin\n"), "k")
	f.Fuzz(func(t *testing.T, data []byte, query string) {
		checkMatches(t, data, query)
	})
}

// ---- Benchmarks ----

func benchData() []byte {
	var b bytes.Buffer
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "# kuroko:cmd:2026-06-18T12:00:01+09:00\nuser@host:~$ cmd%d\nGigabitEthernet0/%d is up, LINE protocol is up\nsome Output line here\n", i, i)
	}
	return b.Bytes()
}

func BenchmarkParseMetadata(b *testing.B) {
	data := benchData()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := &Viewer{logData: data}
		v.parseMetadata()
	}
}

func BenchmarkOldParseMetadata(b *testing.B) {
	data := benchData()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		oldParseMetadata(data)
	}
}

func BenchmarkSplitOutputLines(b *testing.B) {
	data := benchData()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		splitOutputLines(data)
	}
}

func BenchmarkOldOutputLines(b *testing.B) {
	data := benchData()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		oldOutputLines(b, data)
	}
}

func BenchmarkUpdateMatches(b *testing.B) {
	v := &Viewer{currentOutputLines: splitOutputLines(benchData()), outputQuery: "LINE protocol"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v.updateMatches()
	}
}

func BenchmarkOldMatches(b *testing.B) {
	lines := oldOutputLines(b, benchData())
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		oldMatches(lines, "LINE protocol")
	}
}

func compressForTest(t testing.TB, src string) string {
	t.Helper()
	gz, err := logstore.CompressFile(src)
	if err != nil {
		t.Fatal(err)
	}
	return gz
}

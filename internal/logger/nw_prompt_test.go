package logger

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// nwPromptRe is the reference specification (and test oracle) for
// matchNWPrompt. Groups: 1=hostname 2=(mode-parens) 3=mode 4=indicator.
var nwPromptRe = regexp.MustCompile(
	`^([A-Za-z][A-Za-z0-9._@-]*)` +
		`(\(([A-Za-z0-9][A-Za-z0-9._-]*)\))?` +
		`([#>])`)

// legacySplitPromptInfo is the pre-optimisation implementation, kept verbatim
// as an oracle.
func legacySplitPromptInfo(line []byte) (prompt, cmd []byte, info PromptInfo) {
	if p, c := SplitPrompt(line); p != nil {
		return p, c, PromptInfo{Kind: KindShell}
	}
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '!' {
		return nil, nil, PromptInfo{}
	}
	if strings.HasPrefix(string(line), "# kuroko:") {
		return nil, nil, PromptInfo{}
	}
	m := nwPromptRe.FindSubmatchIndex(line)
	if m == nil {
		return nil, nil, PromptInfo{}
	}
	hostname := string(line[m[2]:m[3]])
	mode := ""
	if m[6] >= 0 {
		mode = string(line[m[6]:m[7]])
	}
	indicator := line[m[8]]
	var kind PromptKind
	switch {
	case mode != "":
		kind = KindConfig
	case indicator == '#':
		kind = KindPrivileged
	default:
		kind = KindUser
	}
	if strings.Contains(hostname, "://") {
		return nil, nil, PromptInfo{}
	}
	if isAllDigits(hostname) {
		return nil, nil, PromptInfo{}
	}
	promptEnd := m[1]
	promptBytes := line[:promptEnd]
	var cmdBytes []byte
	if promptEnd < len(line) {
		cmdBytes = line[promptEnd:]
	}
	return promptBytes, cmdBytes, PromptInfo{Kind: kind, Hostname: hostname, Mode: mode}
}

var nwPromptCorpus = []string{
	"Router#", "Router>", "Router(config-if)#", "sw1(config-router)# show",
	"admin@junos>", "edge-sw01.dc1#",
	"GigabitEthernet0/1 is up, line protocol is up",
	"Router(", "Router()#", "Router(-x)#", "Router(config", "Router(config)",
	"Router(config)x", "Router(a b)#", "Router((a)#", "Router(a))#",
	"1Router#", "R#", "R", "", "R@@x>", "Rout er#", "Rötr#", "R\xff#",
	"R(\xff)#", "R(é)#", "Router#show version", "Router>enable",
	"http://x#", "x(1)#", "a.b_c-d@e(f.g_h-i)>cmd", "9#", "R(0)>",
	"# kuroko: start", " Router#", "!x#", "user@host:~$ ls",
	strings.Repeat("a", 5000) + "#",
	strings.Repeat("a", 5000),
	strings.Repeat("a", 5000) + "(" + strings.Repeat("b", 5000) + ")>",
	strings.Repeat("a", 5000) + "(" + strings.Repeat("b", 5000),
	strings.Repeat("a-", 3000) + "x",
}

func TestMatchNWPromptMatchesRegex(t *testing.T) {
	for _, s := range nwPromptCorpus {
		line := []byte(s)
		want := nwPromptRe.FindSubmatchIndex(line)
		got := matchNWPrompt(line)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("matchNWPrompt(%.40q) = %v, regex = %v", s, got, want)
		}
	}
}

func TestSplitPromptInfoMatchesLegacy(t *testing.T) {
	for _, s := range nwPromptCorpus {
		assertSameSplit(t, []byte(s))
	}
}

func assertSameSplit(t *testing.T, line []byte) {
	t.Helper()
	gp, gc, gi := SplitPromptInfo(line)
	wp, wc, wi := legacySplitPromptInfo(line)
	if !bytes.Equal(gp, wp) || !bytes.Equal(gc, wc) || (gp == nil) != (wp == nil) || (gc == nil) != (wc == nil) || gi != wi {
		t.Fatalf("mismatch for %q: got (%q,%q,%+v) want (%q,%q,%+v)", line, gp, gc, gi, wp, wc, wi)
	}
}

func FuzzNWPromptMatchesRegex(f *testing.F) {
	for _, s := range nwPromptCorpus {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		want := nwPromptRe.FindSubmatchIndex(line)
		got := matchNWPrompt(line)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("matchNWPrompt(%q) = %v, regex = %v", line, got, want)
		}
	})
}

func FuzzSplitPromptInfoMatchesLegacy(f *testing.F) {
	for _, s := range nwPromptCorpus {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		assertSameSplit(t, line)
	})
}

var benchSplitSink PromptInfo

func BenchmarkSplitPromptInfoInterfaceLine(b *testing.B) {
	line := []byte("GigabitEthernet0/1 is up, line protocol is up (connected)")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, benchSplitSink = SplitPromptInfo(line)
	}
}

func BenchmarkSplitPromptInfoPromptLine(b *testing.B) {
	line := []byte("edge-sw01.dc1(config-if)#show running-config")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, benchSplitSink = SplitPromptInfo(line)
	}
}

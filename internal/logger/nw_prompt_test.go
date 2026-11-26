package logger

import (
	"bytes"
	"os"
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

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
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

func TestSplitPromptInfoNetwork(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantHost string
		wantMode string
		wantKind PromptKind
		wantCmd  string
	}{
		{
			name:     "cisco privileged with command",
			line:     "Router#show version",
			wantHost: "Router",
			wantMode: "",
			wantKind: KindPrivileged,
			wantCmd:  "show version",
		},
		{
			name:     "cisco user mode",
			line:     "Router>enable",
			wantHost: "Router",
			wantMode: "",
			wantKind: KindUser,
			wantCmd:  "enable",
		},
		{
			name:     "cisco bare privileged prompt",
			line:     "switch#",
			wantHost: "switch",
			wantMode: "",
			wantKind: KindPrivileged,
			wantCmd:  "",
		},
		{
			name:     "cisco config mode",
			line:     "R1(config)#hostname R2",
			wantHost: "R1",
			wantMode: "config",
			wantKind: KindConfig,
			wantCmd:  "hostname R2",
		},
		{
			name:     "cisco config-if mode",
			line:     "R1(config-if)#no shutdown",
			wantHost: "R1",
			wantMode: "config-if",
			wantKind: KindConfig,
			wantCmd:  "no shutdown",
		},
		{
			name:     "cisco config-router mode",
			line:     "core-sw01(config-router)#neighbor 10.0.0.2 remote-as 65001",
			wantHost: "core-sw01",
			wantMode: "config-router",
			wantKind: KindConfig,
			wantCmd:  "neighbor 10.0.0.2 remote-as 65001",
		},
		{
			name:     "juniper operational no space",
			line:     "user@junos>show route",
			wantHost: "user@junos",
			wantMode: "",
			wantKind: KindUser,
			wantCmd:  "show route",
		},
		{
			name:     "hostname with dots and dashes",
			line:     "edge-rtr-01.dc1#show ip int brief",
			wantHost: "edge-rtr-01.dc1",
			wantMode: "",
			wantKind: KindPrivileged,
			wantCmd:  "show ip int brief",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, cmd, info := SplitPromptInfo([]byte(tt.line))
			if info.Kind != tt.wantKind {
				t.Fatalf("Kind = %v, want %v (prompt=%q)", info.Kind, tt.wantKind, string(prompt))
			}
			if info.Hostname != tt.wantHost {
				t.Errorf("Hostname = %q, want %q", info.Hostname, tt.wantHost)
			}
			if info.Mode != tt.wantMode {
				t.Errorf("Mode = %q, want %q", info.Mode, tt.wantMode)
			}
			if strings.TrimSpace(string(cmd)) != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", strings.TrimSpace(string(cmd)), tt.wantCmd)
			}
		})
	}
}

func TestSplitPromptInfoNetworkRejects(t *testing.T) {
	rejects := []string{
		"# kuroko:cmd:2026-06-26T10:00:00+09:00",
		"100>50",
		"  Router#show version",
		"Building configuration...",
		"!",
		"http://example.com#anchor",
		"interface GigabitEthernet0/1 is up",
	}
	for _, line := range rejects {
		_, _, info := SplitPromptInfo([]byte(line))
		if info.Kind == KindUser || info.Kind == KindPrivileged || info.Kind == KindConfig {
			t.Errorf("SplitPromptInfo(%q) wrongly detected network prompt (kind=%v)", line, info.Kind)
		}
	}
}

func TestSplitPromptInfoBashStillWorks(t *testing.T) {
	bash := []string{
		"user@host:~$ ls",
		"containerlab-vm# uptime",
		"[user@host path]$ pwd",
	}
	for _, line := range bash {
		prompt, _, info := SplitPromptInfo([]byte(line))
		if info.Kind != KindShell {
			t.Errorf("SplitPromptInfo(%q) Kind = %v, want KindShell (prompt=%q)", line, info.Kind, string(prompt))
		}
	}
}

func TestLoggerNetworkModeCommandExtraction(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, []string{"ssh", "admin@router"}, false)
	if err != nil {
		t.Fatal(err)
	}
	l.Write([]byte("Router#show version\r\n"))
	l.Write([]byte("Cisco IOS Software\r\n"))
	l.Write([]byte("Router#\r\n"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "# kuroko:cmd:") {
		t.Errorf("expected a command marker for network prompt, got:\n%s", s)
	}
	if !strings.Contains(s, "Router#show version") {
		t.Errorf("expected command line preserved, got:\n%s", s)
	}
}

func TestLoggerBashModeNoNetworkFalsePositive(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	l.Write([]byte("foo#bar baz\r\n"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "# kuroko:cmd:") {
		t.Errorf("network prompt detection leaked into bash mode:\n%s", s)
	}
}

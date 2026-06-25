package logger

import (
	"os"
	"strings"
	"testing"
)

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

func TestSplitPromptBackwardCompat(t *testing.T) {
	prompt, cmd := SplitPrompt([]byte("user@host:~$ ls -la"))
	if string(prompt) != "user@host:~$ " {
		t.Errorf("prompt = %q, want %q", string(prompt), "user@host:~$ ")
	}
	if string(cmd) != "ls -la" {
		t.Errorf("cmd = %q, want %q", string(cmd), "ls -la")
	}

	p, c := SplitPrompt([]byte("just some output"))
	if p != nil || c != nil {
		t.Errorf("expected nil,nil for non-prompt, got %q,%q", string(p), string(c))
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

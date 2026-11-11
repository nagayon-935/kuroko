package sshconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, home, name, content string) {
	t.Helper()
	path := filepath.Join(home, ".ssh", name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSSHHosts(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, "config", `# ignored
Host = router "Switch" router *.example !excluded ?test zebra Alpha alpha
  HostName not-an-alias
Include "conf.d/*" missing ~
Match exec "must never run"
  Include ~/extra-does-not-exist
hOsT='quoted' # comment
Host -option
Host evil`+"\u202e"+`host
`)
	writeConfig(t, home, "conf.d/one", "Host included\nInclude config\n")
	writeConfig(t, home, "conf.d/two", "Host=second\nHost router\n")
	// Subdirectories, symlink loops, and device nodes inside conf.d/* must be skipped safely.
	if err := os.MkdirAll(filepath.Join(home, ".ssh", "conf.d", "subdir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".ssh", "config"), filepath.Join(home, ".ssh", "conf.d", "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(home, ".ssh", "conf.d", "devnull")); err != nil {
		t.Fatal(err)
	}
	hosts, err := Hosts(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Alpha", "alpha", "included", "quoted", "router", "second", "Switch", "zebra"}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("hosts = %q, want %q", hosts, want)
	}
}

func TestValidHostSecurity(t *testing.T) {
	for _, invalid := range []string{
		"",
		"-oProxyCommand=sh",
		"--",
		"host name",
		"host\tname",
		"host\nname",
		"host\x00name",
		"host\x1b[2J",
		"host\u202ename",
		"*.example.com",
		"host?1",
		"!negated",
		"bad\xffutf8",
		"user@-oProxyCommand=sh",
		"@router",
		"user@",
	} {
		if ValidHost(invalid) {
			t.Errorf("ValidHost(%q) = true; want false", invalid)
		}
	}
	for _, valid := range []string{"router", "admin@192.0.2.1", "edge-sw01.example.jp"} {
		if !ValidHost(valid) {
			t.Errorf("ValidHost(%q) = false; want true", valid)
		}
	}
}

func TestMissingSSHConfig(t *testing.T) {
	hosts, err := Hosts(t.TempDir())
	if err != nil || len(hosts) != 0 {
		t.Fatalf("hosts = %q, error = %v", hosts, err)
	}
}

func TestSSHPreviewSources(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "config", "Include conf.d/*\nHost router\n  HostName 192.0.2.1\n  User admin\nHost other\n  Port 2222\n")
	writeConfig(t, home, "conf.d/edge", "Host edge\n  HostName 192.0.2.2\n  IdentityFile ~/.ssh/key\nMatch exec never-run-this\n  User hidden\n")
	_, previews, err := Entries(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{filepath.Join(home, ".ssh/config") + ":2", "HostName 192.0.2.1", "User admin"} {
		if !strings.Contains(previews["router"], want) {
			t.Errorf("router preview lacks %q: %s", want, previews["router"])
		}
	}
	if strings.Contains(previews["router"], "Port 2222") || strings.Contains(previews["edge"], "User hidden") {
		t.Fatal("preview includes another block")
	}
	if !strings.Contains(previews["edge"], "IdentityFile ~/.ssh/key") {
		t.Fatal("missing included file values")
	}
}

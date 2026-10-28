package launcher

import (
	"bytes"
	"errors"
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
	hosts, err := sshHosts(home)
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
	} {
		if validHost(invalid) {
			t.Errorf("validHost(%q) = true; want false", invalid)
		}
	}
	for _, valid := range []string{"router", "admin@192.0.2.1", "edge-sw01.example.jp"} {
		if !validHost(valid) {
			t.Errorf("validHost(%q) = false; want true", valid)
		}
	}
}

func TestMissingSSHConfig(t *testing.T) {
	hosts, err := sshHosts(t.TempDir())
	if err != nil || len(hosts) != 0 {
		t.Fatalf("hosts = %q, error = %v", hosts, err)
	}
}

func TestSerialDevices(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"tty.usbserial", "tty.usbmodem", "tty.Bluetooth-Incoming-Port", "cu.usbserial", "ttyUSB0", "ttyACM0", "unrelated"} {
		if err := os.Symlink("/dev/null", filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tty.usbregular"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "tty.usbdirectory"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := serialDevices(root)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, name := range []string{"tty.usbmodem", "tty.usbserial"} {
		want = append(want, filepath.Join(root, name))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("devices = %q, want %q", got, want)
	}
}

func TestChoose(t *testing.T) {
	home, dev := t.TempDir(), t.TempDir()
	writeConfig(t, home, "config", "Host router\n")
	device := filepath.Join(dev, "tty.usbserial")
	if err := os.Symlink("/dev/null", device); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, input string
		want        []string
	}{

		{"ssh back to screen", "\n\u0002\u001b[B\n\n\n", []string{"screen", device, "115200"}},
		{"screen back to ssh", "\u001b[B\n\u0002\n\n", []string{"ssh", "router"}},
		{"manual ssh back", "\n\u000e\u0002\n", []string{"ssh", "router"}},
		{"invalid host back", "\n\u000e-option\n\u0002\n", []string{"ssh", "router"}},
		{"manual device back", "\u001b[B\n\u000e\u0002\n\n", []string{"screen", device, "115200"}},
		{"invalid device back", "\u001b[B\n\u000e/missing\n\u0002\n\n", []string{"screen", device, "115200"}},
		{"baud back to devices", "\u001b[B\n\n\u0002\n9600\n", []string{"screen", device, "9600"}},
		{"baud back to connection", "\u001b[B\n\n\u0002\u0002\n\n", []string{"ssh", "router"}},
		{"ssh alias", "\n\n", []string{"ssh", "router"}},
		{"manual ssh", "\n\u000eadmin@example.com\n", []string{"ssh", "admin@example.com"}},
		{"manual host starting b", "\n\u000ebackup\n", []string{"ssh", "backup"}},
		{"manual host starting q", "\n\u000eqa-host\n", []string{"ssh", "qa-host"}},
		{"manual host ignores arrow keys", "\n\u000erou\x1b[A\x1b[Dter\n", []string{"ssh", "router"}},
		{"invalid host", "\n\u000e-oProxyCommand=bad\n\nrouter\n", []string{"ssh", "router"}},
		{"screen default", "\u001b[B\n\n\n", []string{"screen", device, "115200"}},
		{"screen arrows", "\u001b[B\u001b[A\u001b[B\n\n\n", []string{"screen", device, "115200"}},
		{"screen baud validation", "\u001b[B\n\n0\nabc\n9600\n", []string{"screen", device, "9600"}},
		{"manual device validation", "\x1b[B\n\x0e/missing\n" + device + "\n38400\n", []string{"screen", device, "38400"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Unread bytes must be preserved for the child session.
			in := strings.NewReader(tt.input + "session input\n")
			var out bytes.Buffer
			got, err := choose(in, &out, home, dev)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("choose = %q, %v; want %q; output length: %d", got, err, tt.want, out.Len())
			}
			if in.Len() != len("session input\n") {
				t.Fatalf("consumed session input: %d bytes remain", in.Len())
			}
		})
	}
}

func TestChooseCancellation(t *testing.T) {
	home, dev := t.TempDir(), t.TempDir()
	if err := os.Symlink("/dev/null", filepath.Join(dev, "tty.usbserial")); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"", "\x03", "\n\x03", "\n\x0e\x03", "\x1b[B\n\x0e/dev/null\n\x03"} {
		_, err := choose(strings.NewReader(input), &bytes.Buffer{}, home, dev)
		if !errors.Is(err, errCancelled) {
			t.Errorf("input %q: error = %v", input, err)
		}
	}
}

func TestChooseWithoutSerialDevice(t *testing.T) {
	home, dev := t.TempDir(), t.TempDir()
	writeConfig(t, home, "config", "Host router\n")
	// Non-USB devices must not cause the Connection screen to appear.
	if err := os.Symlink("/dev/null", filepath.Join(dev, "tty.Bluetooth-Incoming-Port")); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"\n", "\x02\n\n"} {
		var out bytes.Buffer
		got, err := choose(strings.NewReader(input), &out, home, dev)
		if err != nil || !reflect.DeepEqual(got, []string{"ssh", "router"}) {
			t.Fatalf("choose = %q, %v; output: %s", got, err, &out)
		}
		if !strings.HasPrefix(out.String(), "\x1b[2J\x1b[H\x1b[HSSH host") {
			t.Fatalf("did not go directly to SSH: %q", out.String())
		}
	}
}

func TestConnectionScreenCleared(t *testing.T) {
	home, dev := t.TempDir(), t.TempDir()
	writeConfig(t, home, "config", "Host router\n")
	if err := os.Symlink("/dev/null", filepath.Join(dev, "tty.usbserial")); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"\n\x03", "\x1b[B\n\x03"} {
		var out bytes.Buffer
		_, err := choose(strings.NewReader(input), &out, home, dev)
		if !errors.Is(err, errCancelled) {
			t.Fatal(err)
		}
		title := "SSH host"
		if strings.HasPrefix(input, "\x1b[B") {
			title = "Serial device"
		}
		if !strings.Contains(out.String(), "\x1b[2J\x1b[H\x1b[H"+title) {
			t.Fatalf("Connection screen was not cleared: %q", out.String())
		}
	}
}

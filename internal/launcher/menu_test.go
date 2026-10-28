package launcher

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/ryu/kuroko/internal/textwidth"
)

func TestSSHPreviewSources(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "config", "Include conf.d/*\nHost router\n  HostName 192.0.2.1\n  User admin\nHost other\n  Port 2222\n")
	writeConfig(t, home, "conf.d/edge", "Host edge\n  HostName 192.0.2.2\n  IdentityFile ~/.ssh/key\nMatch exec never-run-this\n  User hidden\n")
	_, previews, err := sshEntries(home)
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

func TestMenuNavigationAndSearch(t *testing.T) {
	items := []string{"alpha", "backup", "mbq-host", "qa-router"}
	for _, tt := range []struct{ keys, want string }{
		{"\u001b[B\n", "backup"},
		{"QR\n", "qa-router"},
		{"backup\n", "backup"},
		{"mbq\n", "mbq-host"},
		{"MBQ\n", "mbq-host"},
		{"none\u0015alpha\n", "alpha"},
		{"a\u001b[B\n", "backup"},
		{"\u000eqa-host\n", "qa-host"},
		{"jku\u0015mbq\n", "mbq-host"},
		{"\u001b[6~\u001b[5~mbq\n", "mbq-host"},
	} {
		var out bytes.Buffer
		p := prompt{in: strings.NewReader(tt.keys), out: &out}
		got, err := p.selectItem("SSH host", items, true, true)
		if err != nil || got != tt.want {
			t.Errorf("keys %q: got %q, %v; want %q", tt.keys, got, err, tt.want)
		}
	}
	for keys, want := range map[string]error{"\x03": errCancelled, "\x02": errBack, "mbq\x03": errCancelled, "mbq\x02": errBack} {
		in := strings.NewReader(keys + "untouched")
		p := prompt{in: in, out: &bytes.Buffer{}}
		_, err := p.selectItem("SSH host", items, true, true)
		if !errors.Is(err, want) || in.Len() != len("untouched") {
			t.Errorf("single-key %q: error %v, %d bytes remain", keys, err, in.Len())
		}
	}
}

func TestPreviewRendering(t *testing.T) {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	for _, width := range []int{40, 80, 120} {
		var out bytes.Buffer
		p := prompt{out: &out, previews: map[string]string{"router": "config:1\nHost router\n  HostName 192.0.2.1\n  User admin\n\x1b[2Junsafe"}}
		p.drawMenu("SSH host", []string{"router"}, 0, 0, 0, width, 24, "", true, true)
		frame := ansi.ReplaceAllString(out.String(), "")
		if !strings.Contains(frame, "HostName 192.0.2.1") || !strings.Contains(frame, "User admin") {
			t.Fatalf("width %d: missing preview values", width)
		}
		if strings.Contains(out.String(), "\x1b[2Junsafe") {
			t.Fatal("config escape sequence was emitted")
		}
		for _, row := range strings.Split(frame, "\r\n") {
			if got := textwidth.String(row); got > width-1 {
				t.Fatalf("width %d: row width %d exceeds terminal: %q", width, got, row)
			}
		}
	}
}

func TestPreviewWrappingAndScrolling(t *testing.T) {
	if got := wrapPreview("abcdef\n日本語", 4); !reflect.DeepEqual(got, []string{"abcd", "ef", "日本", "語"}) {
		t.Fatalf("wrapped = %q", got)
	}
	var out bytes.Buffer
	p := prompt{out: &out, previews: map[string]string{"router": strings.Repeat("setting\n", 30) + "last-setting"}}
	scroll := p.drawMenu("SSH host", []string{"router"}, 0, 0, 999, 80, 24, "", true, true)
	if !strings.Contains(out.String(), "last-setting") || scroll >= 999 {
		t.Fatal("preview scroll was not clamped to last line")
	}
}

package logger

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// screen uses the alternate screen for the entire connection, including
// ordinary serial-console output. Only editor-like sessions should suppress it.
func TestLoggerScreenOutput(t *testing.T) {
	for _, command := range []string{"screen", "/usr/bin/screen", "ssh", "bash"} {
		for _, chunkSize := range []int{1, 3, 4096} {
			t.Run(command+"/chunks="+fmt.Sprint(chunkSize), func(t *testing.T) {
				l, err := New(t.TempDir(), []string{command, "/dev/ttyUSB0", "115200"}, true)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = l.Close(0) })
				// Startup and shutdown sequences captured from screen on xterm.
				stream := "before\r\n\x1b[?1049h\x1b[!p\x1b[?3;4l\x1b[4l\x1b>" +
					"\x1b[?1h\x1b=\x1b[0m\x1b(B\x1b[1;24r\x1b[H\x1b[2J" +
					"edgeSW03# show version\r\n機器の実行結果\r\nBearer screen-secret\r\n" +
					"\x1b[?1l\x1b>\x1b[24;1H\r\n\x1b[?1049lafter\r\n"
				for len(stream) > 0 {
					n := min(chunkSize, len(stream))
					if _, err := l.Write([]byte(stream[:n])); err != nil {
						t.Fatal(err)
					}
					stream = stream[n:]
				}
				if l.InAltScreen() {
					t.Fatal("alternate screen state was not cleared")
				}
				if err := l.Close(0); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(l.Path)
				if err != nil {
					t.Fatal(err)
				}
				content := string(data)
				wantOutput := command == "screen" || command == "/usr/bin/screen"
				for _, output := range []string{"edgeSW03# show version", "機器の実行結果"} {
					if strings.Contains(content, output) != wantOutput {
						t.Errorf("output %q: want present=%v, got:\n%s", output, wantOutput, content)
					}
				}
				if !strings.Contains(content, "after") || strings.Contains(content, "\x1b") || strings.Contains(content, "screen-secret") {
					t.Errorf("unfiltered or missing output:\n%s", content)
				}
				if wantOutput && (!strings.Contains(content, "# kuroko:cmd:") || !strings.Contains(content, "[TOKEN_REDACTED]")) {
					t.Errorf("screen output lost command metadata or redaction:\n%s", content)
				}
			})
		}
	}
}

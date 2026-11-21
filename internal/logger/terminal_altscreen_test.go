package logger

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func runAndReadLog(t *testing.T, args []string, feed func(l *Logger)) string {
	t.Helper()
	l, err := New(t.TempDir(), args, false)
	if err != nil {
		t.Fatal(err)
	}
	feed(l)
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAltScreenModesDiscardFullScreenOutput(t *testing.T) {
	modes := []string{"47", "1047", "1049"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			stream := "before\r\n\x1b[?" + mode + "hPAINT-INSIDE\r\nmore-inside\r\n\x1b[?" + mode + "lafter\r\n"

			// Act
			got := runAndReadLog(t, []string{"bash"}, func(l *Logger) {
				if _, err := l.Write([]byte(stream)); err != nil {
					t.Fatal(err)
				}
			})

			// Assert
			if strings.Contains(got, "INSIDE") || strings.Contains(got, "?"+mode) {
				t.Errorf("alternate screen output leaked:\n%s", got)
			}
			if !strings.Contains(got, "before\n") || !strings.Contains(got, "after\n") {
				t.Errorf("text outside alternate screen lost:\n%s", got)
			}
		})
	}
}

func TestAltScreenModesAcceptMixedEnterAndExit(t *testing.T) {
	// Arrange: enter with 47, leave with 1049.
	stream := "a\r\n\x1b[?47hHIDDEN\r\n\x1b[?1049lb\r\n"

	// Act
	got := runAndReadLog(t, []string{"bash"}, func(l *Logger) {
		if _, err := l.Write([]byte(stream)); err != nil {
			t.Fatal(err)
		}
	})

	// Assert
	if strings.Contains(got, "HIDDEN") || !strings.Contains(got, "a\n") || !strings.Contains(got, "b\n") {
		t.Errorf("unexpected log:\n%s", got)
	}
}

func TestAltScreenSplitAtEveryByteBoundary(t *testing.T) {
	for _, mode := range []string{"47", "1047", "1049"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			stream := "before\r\n\x1b[?" + mode + "hINSIDE\r\n\x1b[?" + mode + "lafter\r\n"

			// Act: one byte per Write.
			got := runAndReadLog(t, []string{"bash"}, func(l *Logger) {
				for i := 0; i < len(stream); i++ {
					if _, err := l.Write([]byte{stream[i]}); err != nil {
						t.Fatal(err)
					}
				}
			})

			// Assert
			if strings.Contains(got, "INSIDE") || strings.Contains(got, "[?") {
				t.Errorf("leak with byte-wise writes:\n%s", got)
			}
			if !strings.Contains(got, "before\n") || !strings.Contains(got, "after\n") {
				t.Errorf("outside text lost:\n%s", got)
			}
		})
	}
}

func TestAltScreenStillLoggedForScreenWrapper(t *testing.T) {
	for _, mode := range []string{"47", "1047", "1049"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			stream := "\x1b[?" + mode + "hconnection output\r\n\x1b[?" + mode + "l"

			// Act
			got := runAndReadLog(t, []string{"screen", "/dev/ttyUSB0"}, func(l *Logger) {
				if _, err := l.Write([]byte(stream)); err != nil {
					t.Fatal(err)
				}
			})

			// Assert
			if !strings.Contains(got, "connection output\n") {
				t.Errorf("screen output not logged:\n%s", got)
			}
		})
	}
}

func TestLoggerAltScreenSuppressed(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Simulate: shell prompt, then vim opens (alt screen), then vim closes, then prompt again.
	l.Write([]byte("user@host:~$ vim file.txt\r\n"))
	l.Write([]byte("\x1b[?1049h")) // enter alternate screen
	l.Write([]byte("~\r\n~\r\n\"file.txt\" 0L, 0B\r\n"))
	l.Write([]byte("\x1b[?1049l")) // exit alternate screen
	l.Write([]byte("user@host:~$ \r\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if strings.Contains(content, "\"file.txt\" 0L") {
		t.Errorf("vim screen content should be suppressed, got:\n%s", content)
	}
	if !strings.Contains(content, "vim file.txt") {
		t.Errorf("shell command before vim should be logged, got:\n%s", content)
	}
	if strings.Contains(content, "full-screen app active") {
		t.Errorf("suppression marker should not be logged, got:\n%s", content)
	}
}

// TestInAltScreen verifies the InAltScreen() accessor reflects alt-screen state.
func TestInAltScreen(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer l.Close(0)

	if l.InAltScreen() {
		t.Error("InAltScreen() should be false initially")
	}
	l.Write([]byte("\x1b[?1049h"))
	if !l.InAltScreen() {
		t.Error("InAltScreen() should be true after entering alt screen")
	}
	l.Write([]byte("\x1b[?1049l"))
	if l.InAltScreen() {
		t.Error("InAltScreen() should be false after exiting alt screen")
	}
}

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

package logger

import (
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

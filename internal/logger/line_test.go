package logger

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLoggerBackspace(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// User typed "helo", hit backspace, then "lo" — result should be "hello"
	l.Write([]byte("helo\x08lo\n"))
	l.Close(0)

	data, _ := os.ReadFile(l.Path)
	content := string(data)

	if !strings.Contains(content, "hello") {
		t.Errorf("expected 'hello' after backspace correction, got:\n%s", content)
	}
}

// TestLoggerCursorMovement covers ESC[C (cursor forward) and ESC[D (cursor backward).
func TestLoggerCursorMovement(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       string
		wantAbsent string
	}{
		{
			name: "cursor forward then overwrite",
			// "hello", back 3 (col=2), forward 1 (col=3), write "XY" → "helXY"
			input: "hello\x1b[3D\x1b[1CXY\n",
			want:  "helXY",
		},
		{
			name: "cursor backward negative clamp",
			// "hi" (lineCol=2), back 99 → clamped to 0, write 'X' at 0. Nothing
			// erased the 'i', so it is still on screen, exactly as on a real
			// terminal; the row is "Xi". (A cursor moved back by CSI D keeps
			// the text to its right — see lineBuffer.text.)
			input: "hi\x1b[99DX\n",
			want:  "Xi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			l, err := New(tmp, []string{"ssh", "host"}, false)
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			l.Write([]byte(tt.input))
			l.Close(0)

			data, err := os.ReadFile(l.Path)
			if err != nil {
				t.Fatalf("ReadFile %s: %v", l.Path, err)
			}
			content := string(data)
			if !strings.Contains(content, tt.want) {
				t.Errorf("expected %q in log, got:\n%s", tt.want, content)
			}
			if tt.wantAbsent != "" && strings.Contains(content, tt.wantAbsent) {
				t.Errorf("expected %q to be absent from log, got:\n%s", tt.wantAbsent, content)
			}
		})
	}
}

// TestLoggerDeleteChars covers ESC[P (delete character at cursor position).
func TestLoggerDeleteChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "delete one char mid-line",
			// "hello world" (11 chars, lineCol=11), back 5 → lineCol=6 ('w'),
			// delete 1 → lineBuf="hello orld" (10 chars), forward 4 → lineCol=10,
			// '\n' commits lineBuf[:10]="hello orld"
			input: "hello world\x1b[5D\x1b[1P\x1b[4C\n",
			want:  "hello orld",
		},
		{
			name: "delete beyond end truncates to cursor",
			// "hello world" (11 chars), back 5 → col=6, delete 99 → lineBuf[:6] = "hello "
			input: "hello world\x1b[5D\x1b[99P\n",
			want:  "hello ",
		},
		{
			name: "delete at end is no-op",
			// "hello", lineCol=5 == len → condition false, no change
			input: "hello\x1b[1P\n",
			want:  "hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			l, err := New(tmp, []string{"ssh", "host"}, false)
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			l.Write([]byte(tt.input))
			l.Close(0)

			data, err := os.ReadFile(l.Path)
			if err != nil {
				t.Fatalf("ReadFile %s: %v", l.Path, err)
			}
			if !strings.Contains(string(data), tt.want) {
				t.Errorf("expected %q in log, got:\n%s", tt.want, string(data))
			}
		})
	}
}

// TestLoggerJapaneseTextRoundTrips is the baseline: plain multi-byte text
// typed without any cursor editing must survive intact.
func TestLoggerJapaneseTextRoundTrips(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	l.Write([]byte("接続確認スイッチ01\n"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	if !utf8.Valid(data) {
		t.Fatalf("log contains invalid UTF-8:\n%q", data)
	}
	if !strings.Contains(string(data), "接続確認スイッチ01") {
		t.Errorf("expected Japanese text to survive intact, got:\n%s", string(data))
	}
}

// TestLoggerJapaneseBackspaceOverwrite covers B1: readline's in-place
// backspace/overwrite editing when the erased content includes full-width
// (East Asian Wide) runes such as Japanese. lineBuf previously indexed by
// byte, treating each backspace as "move back one byte" — but readline (and
// every real terminal) moves the cursor back one *display column* per
// backspace, and a Japanese character is 2 columns wide but 3 UTF-8 bytes.
// Erasing such a character with the column-correct number of backspaces
// used to land mid-rune and corrupt the encoding.
func TestLoggerJapaneseBackspaceOverwrite(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantAbsent  string
		wantContain string
	}{
		{
			// "あ" occupies 2 terminal columns; stepping back two columns and
			// erasing (how readline deletes a wide character) must fully
			// remove it, not just two of its three UTF-8 bytes.
			name:       "two backspaces and an erase fully remove one full-width kanji",
			input:      "あ\x08\x08\x1b[K\n",
			wantAbsent: "あ",
		},
		{
			// The tty's ECHOE erase of a wide character: back, blank, back,
			// once per column.
			name:       "backspace-space-backspace per column removes one full-width kanji",
			input:      "あ\x08 \x08\x08 \x08\n",
			wantAbsent: "あ",
		},
		{
			// Pathological case: a single backspace lands exactly between
			// the two display columns of "う". The result must still be
			// valid UTF-8 even though the visual erase is a best-effort
			// (real terminals are also inconsistent here).
			name:        "single backspace mid-wide-rune still yields valid UTF-8",
			input:       "あいう\x08X\n",
			wantContain: "あい",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			l, err := New(tmp, []string{"ssh", "host"}, false)
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			l.Write([]byte(tt.input))
			l.Close(0)

			data, err := os.ReadFile(l.Path)
			if err != nil {
				t.Fatalf("ReadFile %s: %v", l.Path, err)
			}
			if !utf8.Valid(data) {
				t.Fatalf("log contains invalid UTF-8 after backspace edit:\n%q", data)
			}
			content := string(data)
			if tt.wantAbsent != "" && strings.Contains(content, tt.wantAbsent) {
				t.Errorf("expected %q to be fully erased, got:\n%s", tt.wantAbsent, content)
			}
			if tt.wantContain != "" && !strings.Contains(content, tt.wantContain) {
				t.Errorf("expected %q in log, got:\n%s", tt.wantContain, content)
			}
		})
	}
}

// TestLoggerJapaneseCursorMovementCSID covers B1's CSI D (cursor backward)
// case: ESC[nD's parameter n is a column count, so moving left across a
// wide rune requires n=2, not n=1. Before the fix, lineCol was a byte
// index, so "move left 2" landed inside the 3-byte encoding of a Japanese
// character instead of before it, and the subsequent overwrite corrupted
// the sequence.
func TestLoggerJapaneseCursorMovementCSID(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// "あい" (4 columns): move left 2 columns lands exactly before "い",
	// then overwrite it with "XY".
	l.Write([]byte("あい\x1b[2DXY\n"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	if !utf8.Valid(data) {
		t.Fatalf("log contains invalid UTF-8 after CSI D overwrite:\n%q", data)
	}
	content := string(data)
	if !strings.Contains(content, "あXY") {
		t.Errorf("expected column-accurate overwrite %q in log, got:\n%s", "あXY", content)
	}
}

// TestLoggerJapaneseMidLineInsertion covers B1's "mid-line insertion" case:
// a shell that doesn't use insert/delete-character escapes typically
// redraws by moving the cursor back and retyping from the insertion point
// to the end of the line. Inserting a wide rune partway through an ASCII
// line, then retyping the tail, must produce valid UTF-8 with the tail
// intact.
func TestLoggerJapaneseMidLineInsertion(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// "abcdef", back 3 columns (after "abc"), insert "あ" then retype "def".
	l.Write([]byte("abcdef\x1b[3Dあdef\n"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	if !utf8.Valid(data) {
		t.Fatalf("log contains invalid UTF-8 after mid-line insertion:\n%q", data)
	}
	content := string(data)
	if !strings.Contains(content, "abcあdef") {
		t.Errorf("expected %q in log, got:\n%s", "abcあdef", content)
	}
}

func TestLoggerWideRuneSplitOverwrite(t *testing.T) {
	var b lineBuffer
	// Overwriting the first column of a 2-column rune must clear its second column.
	b.put('あ')
	b.home()
	b.put('x')
	b.forward(1)
	if got := string(b.text()); got != "x " {
		t.Errorf("overwrite first half of wide rune: got %q, want %q", got, "x ")
	}

	// Overwriting the second column of a 2-column rune must clear its first column.
	b.reset()
	b.put('あ')
	b.backspace()
	b.put('x')
	if got := string(b.text()); got != " x" {
		t.Errorf("overwrite second half of wide rune: got %q, want %q", got, " x")
	}

	// Erasing to end from the middle of a wide rune must replace its leading half with a space.
	b.reset()
	b.put('あ')
	b.backspace()
	b.eraseToEnd()
	if got := string(b.text()); got != " " {
		t.Errorf("eraseToEnd mid-wide-rune: got %q, want %q", got, " ")
	}

	// Deleting across the middle of two wide runes must clear both orphaned halves.
	b.reset()
	b.put('あ')
	b.put('い')
	b.backward(3) // col 1 (second half of 'あ')
	b.deleteChars(2)
	b.forward(1)
	if got := string(b.text()); got != "  " {
		t.Errorf("deleteChars mid-wide-rune: got %q, want %q", got, "  ")
	}
}

func TestLineBufferWideRuneOverwrite(t *testing.T) {
	var b lineBuffer
	for _, r := range "日本" {
		b.put(r)
	}
	b.backward(2)
	b.put('x')
	b.put('y')
	if got, want := string(b.text()), "日xy"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

// TestCloseCursorPastEnd tests that Close() handles lineCol > len(lineBuf) gracefully.
func TestCloseCursorPastEnd(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, false)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Move cursor 99 columns right beyond "hi" — lineCol becomes 101, lineBuf len=2.
	// Close() must clamp end to len(lineBuf) = 2 and write "hi".
	l.Write([]byte("hi\x1b[99C"))
	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", l.Path, err)
	}
	if !strings.Contains(string(data), "hi") {
		t.Errorf("expected 'hi' in log when cursor past end, got:\n%s", string(data))
	}
}

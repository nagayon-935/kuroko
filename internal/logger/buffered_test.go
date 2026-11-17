package logger

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestWriteMakesScrolledRowsReadableWithoutClose(t *testing.T) {
	// Arrange
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close(0) })
	l.SetScreenRows(5)

	// Act: 20 complete rows, only the last 5 may stay in the redraw window.
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&sb, "row-%02d\r\n", i)
	}
	if _, err := l.Write([]byte(sb.String())); err != nil {
		t.Fatal(err)
	}

	// Assert: rows that left the window are on disk before Close.
	got, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		if want := fmt.Sprintf("row-%02d\n", i); !strings.Contains(string(got), want) {
			t.Errorf("row %d not on disk after Write returned", i)
		}
	}
}

func TestSetScreenRowsFlushesTrimmedRows(t *testing.T) {
	// Arrange: no height known, so all rows stay in the window.
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close(0) })
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sb, "row-%02d\r\n", i)
	}
	if _, err := l.Write([]byte(sb.String())); err != nil {
		t.Fatal(err)
	}

	// Act
	l.SetScreenRows(3)

	// Assert
	got, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "row-06\n") {
		t.Errorf("trimmed rows not on disk after SetScreenRows:\n%s", got)
	}
}

func TestWriteSurfacesFlushErrorAndCloseJoinsIt(t *testing.T) {
	// Arrange
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	l.SetScreenRows(2)
	if err := l.file.Close(); err != nil {
		t.Fatal(err)
	}

	// Act
	_, writeErr := l.Write([]byte("a\r\nb\r\nc\r\nd\r\ne\r\n"))
	closeErr := l.Close(0)

	// Assert
	if !errors.Is(writeErr, os.ErrClosed) {
		t.Errorf("Write error = %v; want ErrClosed", writeErr)
	}
	if !errors.Is(closeErr, os.ErrClosed) {
		t.Errorf("Close error = %v; want ErrClosed", closeErr)
	}
}

func TestCloseWritesFooterThroughBuffer(t *testing.T) {
	// Arrange
	l, err := New(t.TempDir(), []string{"bash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("hello\r\n")); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := l.Close(7); err != nil {
		t.Fatal(err)
	}

	// Assert
	got, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "hello\n") || !strings.HasSuffix(string(got), "# Exit    : 7\n") {
		t.Errorf("log missing body or footer:\n%s", got)
	}
}

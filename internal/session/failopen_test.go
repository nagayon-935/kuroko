package session

import (
	"bytes"
	"errors"
	"testing"
)

type countingErrWriter struct {
	calls int
	err   error
}

func (c *countingErrWriter) Write(p []byte) (int, error) {
	c.calls++
	if c.err != nil {
		return 0, c.err
	}
	return len(p), nil
}

type shortWriter struct{ calls int }

func (s *shortWriter) Write(p []byte) (int, error) {
	s.calls++
	if len(p) == 0 {
		return 0, nil
	}
	return 1, nil // always short: never reports the full length written
}

func TestFailOpenWriterPassesThroughOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	w := newFailOpenWriter(&buf)

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v; want nil", err)
	}
	if n != 5 {
		t.Errorf("Write() n = %d; want 5", n)
	}
	if buf.String() != "hello" {
		t.Errorf("underlying data = %q; want %q", buf.String(), "hello")
	}
}

func TestFailOpenWriterSwallowsError(t *testing.T) {
	inner := &countingErrWriter{err: errors.New("disk full")}
	w := newFailOpenWriter(inner)

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v; want nil (fail-open must swallow the error so io.MultiWriter keeps draining)", err)
	}
	if n != 5 {
		t.Errorf("Write() n = %d; want 5 (must report a full write to satisfy the io.Writer contract)", n)
	}
}

func TestFailOpenWriterStopsRetryingAfterFailure(t *testing.T) {
	inner := &countingErrWriter{err: errors.New("disk full")}
	w := newFailOpenWriter(inner)

	w.Write([]byte("a"))
	w.Write([]byte("b"))
	w.Write([]byte("c"))

	if inner.calls != 1 {
		t.Errorf("underlying Write called %d times; want 1 (should stop hammering a known-failed writer)", inner.calls)
	}
}

func TestFailOpenWriterTreatsShortWriteAsFailure(t *testing.T) {
	short := &shortWriter{}
	w := newFailOpenWriter(short)

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v; want nil", err)
	}
	if n != 5 {
		t.Errorf("Write() n = %d; want 5", n)
	}
	if short.calls != 1 {
		t.Fatalf("underlying Write called %d times; want 1", short.calls)
	}

	w.Write([]byte("world"))
	if short.calls != 1 {
		t.Errorf("underlying Write called %d times after a short write; want 1 (a short write must be treated as failure)", short.calls)
	}
}

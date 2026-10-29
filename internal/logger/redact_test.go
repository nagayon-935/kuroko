package logger

import (
	"errors"
	"os"
	"testing"
)

func TestRedactText(t *testing.T) {
	in := "# Command : aws s3 ls\n" +
		"export AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP\n" +
		"curl -H 'Authorization: Bearer abc.def-ghi'\n" +
		"-----BEGIN OPENSSH PRIVATE KEY-----\n" +
		"b3BlbnNzaC1rZXktdjEAAAAA\n" +
		"-----END OPENSSH PRIVATE KEY-----\n" +
		"plain output"
	want := "# Command : aws s3 ls\n" +
		"export AWS_ACCESS_KEY_ID=[AWS_ACCESS_KEY_REDACTED]\n" +
		"curl -H 'Authorization: Bearer [TOKEN_REDACTED]'\n" +
		"[PEM PRIVATE KEY BLOCK STARTED - REDACTED]\n" +
		"[PEM PRIVATE KEY DATA - REDACTED]\n" +
		"[PEM PRIVATE KEY BLOCK ENDED - REDACTED]\n" +
		"plain output"
	if got := string(RedactText([]byte(in))); got != want {
		t.Fatalf("RedactText =\n%s\nwant\n%s", got, want)
	}
}

func TestLoggerRawDebugSkippedWhenRedacting(t *testing.T) {
	t.Setenv("KUROKO_RAW_DEBUG", "1")
	l, err := New(t.TempDir(), []string{"bash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("export AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.Path + ".raw"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw debug file exists with redaction enabled (stat err = %v)", err)
	}
}

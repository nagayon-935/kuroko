package notifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebhookErrorsDoNotRevealTheURL(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "s.log")
	if err := os.WriteFile(logPath, []byte("output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const token = "SECRET-WEBHOOK-TOKEN"
	tests := []struct {
		name string
		url  string
	}{
		// Port 1 refuses connections; nothing leaves the machine.
		{"unreachable", "http://127.0.0.1:1/api/webhooks/123/" + token},
		{"unparsable", "http://127.0.0.1:1/%zz/" + token},
		{"unsupported scheme", "ftp://127.0.0.1/" + token},
	}
	for _, tt := range tests {
		for _, kind := range []string{"discord", "slack"} {
			t.Run(tt.name+"/"+kind, func(t *testing.T) {
				n := New(Config{Type: kind, WebhookURL: tt.url})

				startErr := n.NotifyStart("ssh host")
				endErr := n.NotifyEnd(logPath, "ssh host", 0, time.Second)

				for _, err := range []error{startErr, endErr} {
					if err == nil {
						t.Fatal("want an error from an unusable webhook URL")
					}
					if strings.Contains(err.Error(), token) {
						t.Errorf("error reveals the webhook token: %v", err)
					}
				}
			})
		}
	}
}

func TestReadLogForSendingKeepsOnlyTheTail(t *testing.T) {
	// Arrange: a log much larger than the limit, ending in a known line.
	logPath := filepath.Join(t.TempDir(), "big.log")
	content := strings.Repeat(strings.Repeat("x", 99)+"\n", 5000) + "last line\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	got, err := readLogForSending(logPath, 1000)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "...(truncated)...\n") || !strings.HasSuffix(got, "last line\n") {
		t.Errorf("got %q…; want a truncation marker and the end of the log", got[:40])
	}
	if body := strings.TrimPrefix(got, "...(truncated)...\n"); len(body) != 1000 {
		t.Errorf("kept %d bytes; want 1000", len(body))
	}
}

func TestReadLogForSendingReturnsShortLogWhole(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "s.log")
	if err := os.WriteFile(logPath, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readLogForSending(logPath, 1000)

	if err != nil || got != "a\nb\n" {
		t.Errorf("readLogForSending = %q, %v; want the whole log", got, err)
	}
}

func TestReadLogForSendingMasksKeyThatBeginsBeforeTheTail(t *testing.T) {
	// Arrange: the BEGIN line falls outside the kept tail.
	logPath := filepath.Join(t.TempDir(), "key.log")
	body := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo0123456789abcdefghijklmnopqr\n", 40)
	content := "-----BEGIN OPENSSH PRIVATE KEY-----\n" + body + "-----END OPENSSH PRIVATE KEY-----\ndone\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readLogForSending(logPath, 300)

	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "QUJDREVG") {
		t.Errorf("key material sent although its BEGIN line was cut off:\n%s", got)
	}
	if !strings.HasSuffix(got, "done\n") {
		t.Errorf("tail lost: %q", got)
	}
}

func TestNotifyEndMasksNetworkDeviceSecrets(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "s.log")
	if err := os.WriteFile(logPath, []byte("Router#show run\nenable secret 5 $1$mERr$hx5rVt7rPNoS4wqbXKX7m0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readLogForSending(logPath, 1000)

	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "$1$mERr") || !strings.Contains(got, "Router#show run") {
		t.Errorf("readLogForSending = %q; want the secret masked and the rest kept", got)
	}
}

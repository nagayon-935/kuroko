package notifier

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfgType string
	}{
		{"discord", "discord"},
		{"slack", "slack"},
		{"noop empty type", ""},
		{"noop unknown type", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := New(Config{Type: tt.cfgType, WebhookURL: "http://example.com"})
			if n == nil {
				t.Fatal("New() returned nil")
			}
		})
	}
}

func TestNoopNotifier(t *testing.T) {
	n := New(Config{})
	if err := n.NotifyStart("cmd"); err != nil {
		t.Errorf("NotifyStart() = %v; want nil", err)
	}
	if err := n.NotifyEnd("/path", "cmd", 0, time.Second); err != nil {
		t.Errorf("NotifyEnd() = %v; want nil", err)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{2*time.Hour + 30*time.Minute + 15*time.Second, "2h 30m 15s"},
		{45*time.Minute + 5*time.Second, "45m 5s"},
		{30 * time.Second, "30s"},
		{0, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q; want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"short unchanged", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"long truncated", "abcdefgh", 3, "...(truncated)...\nfgh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q; want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

func TestDoRequestSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte("body")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := doRequest(req); err != nil {
		t.Errorf("doRequest() = %v; want nil", err)
	}
}

func TestDoRequestBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	err := doRequest(req)
	if err == nil {
		t.Error("expected error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected '400' in error, got: %v", err)
	}
}

func TestDoRequestNetworkError(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1", bytes.NewReader(nil))
	if err := doRequest(req); err == nil {
		t.Error("expected network error, got nil")
	}
}

func TestDiscordNotifyStart(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &discordNotifier{webhookURL: srv.URL}
	if err := d.NotifyStart("ssh host"); err != nil {
		t.Fatalf("NotifyStart() = %v; want nil", err)
	}
	if !strings.Contains(string(capturedBody), "session started") {
		t.Errorf("expected 'session started' in payload, got: %s", capturedBody)
	}
}

func TestDiscordNotifyEnd(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "session.log")
	if err := os.WriteFile(logPath, []byte("log content here"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &discordNotifier{webhookURL: srv.URL}
	// success exit
	if err := d.NotifyEnd(logPath, "ssh host", 0, 30*time.Second); err != nil {
		t.Fatalf("NotifyEnd(exit=0) = %v; want nil", err)
	}
	// non-zero exit (different color/status path)
	if err := d.NotifyEnd(logPath, "ssh host", 1, time.Minute); err != nil {
		t.Fatalf("NotifyEnd(exit=1) = %v; want nil", err)
	}
}

func TestDiscordNotifyEndFileError(t *testing.T) {
	d := &discordNotifier{webhookURL: "http://example.com"}
	err := d.NotifyEnd("/nonexistent/path/file.log", "cmd", 0, time.Second)
	if err == nil {
		t.Error("expected error for missing log file, got nil")
	}
}

func TestSlackNotifyStart(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &slackNotifier{webhookURL: srv.URL}
	if err := s.NotifyStart("ssh host"); err != nil {
		t.Fatalf("NotifyStart() = %v; want nil", err)
	}
	if !strings.Contains(string(capturedBody), "session started") {
		t.Errorf("expected 'session started' in payload, got: %s", capturedBody)
	}
}

func TestSlackNotifyEnd(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "session.log")
	if err := os.WriteFile(logPath, []byte("log content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &slackNotifier{webhookURL: srv.URL}
	if err := s.NotifyEnd(logPath, "ssh host", 0, 10*time.Second); err != nil {
		t.Fatalf("NotifyEnd(exit=0) = %v; want nil", err)
	}
	if err := s.NotifyEnd(logPath, "cmd", 1, time.Minute); err != nil {
		t.Fatalf("NotifyEnd(exit=1) = %v; want nil", err)
	}
}

func TestSlackNotifyEndFileError(t *testing.T) {
	s := &slackNotifier{webhookURL: "http://example.com"}
	err := s.NotifyEnd("/nonexistent/path/file.log", "cmd", 0, time.Second)
	if err == nil {
		t.Error("expected error for missing log file, got nil")
	}
}

package notifier

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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
		{"cut starts at a rune boundary", "あいう", 4, "...(truncated)...\nう"},
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

const secretLog = "# kuroko session log\n" +
	"export AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP\n" +
	"echo '```'\n" +
	"done\n"

// writeGzipLog writes content as a compressed log, as compress_on_close does.
func writeGzipLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.log.gz")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscordNotifyEndSendsRedactedTextOfCompressedLog(t *testing.T) {
	logPath := writeGzipLog(t, secretLog)

	var filename string
	var attachment []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			return
		}
		f, header, err := r.FormFile("files[0]")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			return
		}
		defer f.Close()
		filename = header.Filename
		attachment, _ = io.ReadAll(f)
	}))
	defer srv.Close()

	d := &discordNotifier{webhookURL: srv.URL}
	if err := d.NotifyEnd(logPath, "env AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP ssh host", 0, time.Second); err != nil {
		t.Fatalf("NotifyEnd() = %v", err)
	}
	if filename != "session.log" {
		t.Errorf("attachment name = %q; want the decompressed name session.log", filename)
	}
	if !strings.HasPrefix(string(attachment), "# kuroko session log") {
		t.Errorf("attachment is not the decompressed text: %q", attachment)
	}
	if strings.Contains(string(attachment), "AKIAABCDEFGHIJKLMNOP") || !strings.Contains(string(attachment), "[AWS_ACCESS_KEY_REDACTED]") {
		t.Errorf("attachment was not redacted:\n%s", attachment)
	}
}

func TestSlackNotifyEndSendsRedactedTextOfCompressedLog(t *testing.T) {
	logPath := writeGzipLog(t, secretLog)

	var payload struct{ Text string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decoding payload: %v", err)
		}
	}))
	defer srv.Close()

	s := &slackNotifier{webhookURL: srv.URL}
	if err := s.NotifyEnd(logPath, "env AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP ssh host", 0, time.Second); err != nil {
		t.Fatalf("NotifyEnd() = %v", err)
	}
	if !strings.Contains(payload.Text, "# kuroko session log") {
		t.Errorf("message does not carry the decompressed log text: %q", payload.Text)
	}
	if strings.Contains(payload.Text, "AKIAABCDEFGHIJKLMNOP") {
		t.Errorf("message was not redacted: %q", payload.Text)
	}
	// Only the code block's own fences remain; the log's ``` is defused.
	if got := strings.Count(payload.Text, "```"); got != 2 {
		t.Errorf("message has %d ``` fences; want 2: %q", got, payload.Text)
	}
}

func TestNotifyStartRedactsCommand(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	for _, n := range []Notifier{&discordNotifier{webhookURL: srv.URL}, &slackNotifier{webhookURL: srv.URL}} {
		if err := n.NotifyStart("curl -H 'Authorization: Bearer s3cr3t-token' https://api"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "s3cr3t-token") {
			t.Errorf("%T sent the token: %s", n, body)
		}
	}
}

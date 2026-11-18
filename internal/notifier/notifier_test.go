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
	if err := n.NotifyStart([]string{"cmd"}); err != nil {
		t.Errorf("NotifyStart() = %v; want nil", err)
	}
	if err := n.NotifyEnd("/path", []string{"cmd"}, 0, time.Second); err != nil {
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

	d := New(Config{Type: "discord", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true})
	if err := d.NotifyStart([]string{"ssh", "host"}); err != nil {
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

	d := New(Config{Type: "discord", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true})
	// success exit
	if err := d.NotifyEnd(logPath, []string{"ssh", "host"}, 0, 30*time.Second); err != nil {
		t.Fatalf("NotifyEnd(exit=0) = %v; want nil", err)
	}
	// non-zero exit (different color/status path)
	if err := d.NotifyEnd(logPath, []string{"ssh", "host"}, 1, time.Minute); err != nil {
		t.Fatalf("NotifyEnd(exit=1) = %v; want nil", err)
	}
}

func TestDiscordNotifyEndFileError(t *testing.T) {
	d := New(Config{Type: "discord", WebhookURL: "http://example.com", IncludeArgs: true, IncludeLog: true})
	err := d.NotifyEnd("/nonexistent/path/file.log", []string{"cmd"}, 0, time.Second)
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

	s := New(Config{Type: "slack", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true})
	if err := s.NotifyStart([]string{"ssh", "host"}); err != nil {
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

	s := New(Config{Type: "slack", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true})
	if err := s.NotifyEnd(logPath, []string{"ssh", "host"}, 0, 10*time.Second); err != nil {
		t.Fatalf("NotifyEnd(exit=0) = %v; want nil", err)
	}
	if err := s.NotifyEnd(logPath, []string{"cmd"}, 1, time.Minute); err != nil {
		t.Fatalf("NotifyEnd(exit=1) = %v; want nil", err)
	}
}

func TestSlackNotifyEndFileError(t *testing.T) {
	s := New(Config{Type: "slack", WebhookURL: "http://example.com", IncludeArgs: true, IncludeLog: true})
	err := s.NotifyEnd("/nonexistent/path/file.log", []string{"cmd"}, 0, time.Second)
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

	d := New(Config{Type: "discord", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true})
	if err := d.NotifyEnd(logPath, []string{"env", "AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP", "ssh", "host"}, 0, time.Second); err != nil {
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

	s := New(Config{Type: "slack", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true})
	if err := s.NotifyEnd(logPath, []string{"env", "AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP", "ssh", "host"}, 0, time.Second); err != nil {
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

	for _, n := range []Notifier{New(Config{Type: "discord", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true}), New(Config{Type: "slack", WebhookURL: srv.URL, IncludeArgs: true, IncludeLog: true})} {
		if err := n.NotifyStart([]string{"curl", "-H", "Authorization: Bearer s3cr3t-token", "https://api"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "s3cr3t-token") {
			t.Errorf("%T sent the token: %s", n, body)
		}
	}
}

func TestNotificationOptions(t *testing.T) {
	const secret = "AKIAABCDEFGHIJKLMNOP"
	const masked = "[AWS_ACCESS_KEY_REDACTED]"
	args := []string{"/opt/my tools/ssh", "arg-marker", secret}
	for _, kind := range []string{"discord", "slack"} {
		for _, tt := range []struct {
			name      string
			args, log bool
		}{
			{"default", false, false},
			{"args only", true, false},
			{"log only", false, true},
			{"both", true, true},
		} {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				// A missing log in metadata-only mode also detects an attempted read.
				path := filepath.Join(t.TempDir(), "session.log")
				if tt.log {
					if err := os.WriteFile(path, []byte("log-marker "+secret), 0600); err != nil {
						t.Fatal(err)
					}
				}
				type request struct{ contentType, body string }
				requests := make(chan request, 2)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					requests <- request{r.Header.Get("Content-Type"), string(b)}
				}))
				defer srv.Close()
				n := New(Config{Type: kind, WebhookURL: srv.URL, IncludeArgs: tt.args, IncludeLog: tt.log})

				if err := n.NotifyStart(args); err != nil {
					t.Fatal(err)
				}
				if err := n.NotifyEnd(path, args, 7, 13*time.Second); err != nil {
					t.Fatal(err)
				}

				start, end := <-requests, <-requests
				wantCommand := "`ssh`"
				if tt.args {
					wantCommand = "`/opt/my tools/ssh arg-marker " + masked + "`"
				}
				for _, r := range []request{start, end} {
					if !strings.Contains(r.body, wantCommand) || strings.Contains(r.body, secret) || strings.Contains(r.body, filepath.Dir(path)) {
						t.Errorf("unexpected command, secret or path in payload: %s", r.body)
					}
					if !tt.args && (strings.Contains(r.body, "arg-marker") || strings.Contains(r.body, "/opt/")) {
						t.Errorf("unexpected command arguments or directory: %s", r.body)
					}
				}
				if start.contentType != "application/json" || strings.Contains(start.body, "log-marker") {
					t.Errorf("unexpected start request: %+v", start)
				}
				for _, field := range []string{"exit 7", "13s", "session.log"} {
					if !strings.Contains(end.body, field) {
						t.Errorf("end payload missing %q: %s", field, end.body)
					}
				}
				wantType := "application/json"
				if kind == "discord" && tt.log {
					wantType = "multipart/form-data;"
				}
				if !strings.HasPrefix(end.contentType, wantType) {
					t.Errorf("end Content-Type = %q; want %q", end.contentType, wantType)
				}
				if strings.Contains(end.body, "log-marker "+masked) != tt.log || strings.Contains(end.body, "log-marker") != tt.log {
					t.Errorf("unexpected log content: %s", end.body)
				}
				if strings.Contains(end.body, "```") != (kind == "slack" && tt.log) {
					t.Errorf("unexpected code block: %s", end.body)
				}
				if strings.Contains(end.body, `name="files[0]"; filename="session.log"`) != (kind == "discord" && tt.log) {
					t.Errorf("unexpected attachment: %s", end.body)
				}
			})
		}
	}
}

package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nagayon-935/kuroko/internal/logger"
	"github.com/nagayon-935/kuroko/internal/logstore"
)

// Notifier sends session events to external services.
type Notifier interface {
	// NotifyStart is called immediately when a session begins.
	NotifyStart(command string) error
	// NotifyEnd is called after the session exits, with the completed log file.
	NotifyEnd(logPath, command string, exitCode int, duration time.Duration) error
}

type Config struct {
	Type       string
	WebhookURL string
}

func New(cfg Config) Notifier {
	switch cfg.Type {
	case "discord":
		return &discordNotifier{webhookURL: cfg.WebhookURL}
	case "slack":
		return &slackNotifier{webhookURL: cfg.WebhookURL}
	default:
		return &noopNotifier{}
	}
}

// --- noop ---

type noopNotifier struct{}

func (n *noopNotifier) NotifyStart(string) error                           { return nil }
func (n *noopNotifier) NotifyEnd(string, string, int, time.Duration) error { return nil }

// --- Discord ---

type discordNotifier struct{ webhookURL string }

func (d *discordNotifier) NotifyStart(command string) error {
	embed := map[string]any{
		"title":       "kuroko — session started",
		"color":       0x3498db, // blue
		"description": "🔌 Connected",
		"fields": []any{
			map[string]any{"name": "Command", "value": fmt.Sprintf("`%s`", redactCommand(command)), "inline": false},
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	return d.postEmbed(embed)
}

func (d *discordNotifier) NotifyEnd(logPath, command string, exitCode int, duration time.Duration) error {
	logContent, err := readLogForSending(logPath)
	if err != nil {
		return err
	}
	logContent = []byte(truncate(string(logContent), maxAttachmentBytes))

	color := 0x2ecc71 // green = success
	status := "✅ Success"
	if exitCode != 0 {
		color = 0xe74c3c // red = failure
		status = fmt.Sprintf("❌ Failed (exit %d)", exitCode)
	}

	embed := map[string]any{
		"title":       "kuroko — session ended",
		"color":       color,
		"description": status,
		"fields": []any{
			map[string]any{"name": "Command", "value": fmt.Sprintf("`%s`", redactCommand(command)), "inline": true},
			map[string]any{"name": "Duration", "value": formatDuration(duration), "inline": true},
			map[string]any{"name": "Log file", "value": filepath.Base(logPath), "inline": false},
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	payloadJSON, err := json.Marshal(map[string]any{"embeds": []any{embed}})
	if err != nil {
		return err
	}

	// Attach the log file via multipart
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	pw, err := mw.CreateFormField("payload_json")
	if err != nil {
		return fmt.Errorf("creating payload_json field: %w", err)
	}
	if _, err := pw.Write(payloadJSON); err != nil {
		return fmt.Errorf("writing payload_json field: %w", err)
	}

	// The attachment is the decompressed text, so it drops a .gz suffix.
	fw, err := mw.CreateFormFile("files[0]", strings.TrimSuffix(filepath.Base(logPath), ".gz"))
	if err != nil {
		return fmt.Errorf("creating file field: %w", err)
	}
	if _, err := fw.Write(logContent); err != nil {
		return fmt.Errorf("writing file field: %w", err)
	}

	if err := mw.Close(); err != nil {
		return fmt.Errorf("closing multipart writer: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, d.webhookURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return doRequest(req)
}

func (d *discordNotifier) postEmbed(embed map[string]any) error {
	body, err := json.Marshal(map[string]any{"embeds": []any{embed}})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, d.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doRequest(req)
}

// --- Slack ---

type slackNotifier struct{ webhookURL string }

func (s *slackNotifier) NotifyStart(command string) error {
	return postJSON(s.webhookURL, map[string]string{
		"text": fmt.Sprintf("🔌 *kuroko* session started: `%s`", redactCommand(command)),
	})
}

func (s *slackNotifier) NotifyEnd(logPath, command string, exitCode int, duration time.Duration) error {
	logContent, err := readLogForSending(logPath)
	if err != nil {
		return err
	}
	icon := "✅"
	if exitCode != 0 {
		icon = "❌"
	}
	// A ``` inside the log would close the code block early; a zero-width
	// space keeps it literal.
	excerpt := strings.ReplaceAll(truncate(string(logContent), 2500), "```", "``\u200b`")
	text := fmt.Sprintf("%s *kuroko* `%s` ended (exit %d, %s)\n```%s```",
		icon, redactCommand(command), exitCode, formatDuration(duration), excerpt)
	return postJSON(s.webhookURL, map[string]string{"text": text})
}

// --- helpers ---

// maxAttachmentBytes keeps the Discord attachment below the webhook upload
// limit (10 MiB on most servers); longer logs keep their end.
const maxAttachmentBytes = 8 << 20

// readLogForSending returns the text of the log at logPath, decompressing a
// .gz log, with credentials masked. The content leaves the machine, so it is
// masked even when redaction is off for the local log file.
func readLogForSending(logPath string) ([]byte, error) {
	r, err := logstore.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("reading log: %w", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading log: %w", err)
	}
	return logger.RedactText(data), nil
}

// redactCommand masks credentials in a command line before it is sent.
func redactCommand(command string) string {
	return string(logger.RedactText([]byte(command)))
}

// httpClient is used for all webhook requests. A bounded timeout prevents an
// unreachable or slow webhook from hanging session start/end indefinitely.
var httpClient = &http.Client{Timeout: 10 * time.Second}

func doRequest(req *http.Request) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func postJSON(url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doRequest(req)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// truncate keeps the last max bytes of s, starting at a rune boundary.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	tail := s[len(s)-max:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return "...(truncated)...\n" + tail
}

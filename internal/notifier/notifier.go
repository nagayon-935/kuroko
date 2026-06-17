package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
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

func (n *noopNotifier) NotifyStart(string) error                                    { return nil }
func (n *noopNotifier) NotifyEnd(string, string, int, time.Duration) error          { return nil }

// --- Discord ---

type discordNotifier struct{ webhookURL string }

func (d *discordNotifier) NotifyStart(command string) error {
	embed := map[string]any{
		"title":       "kuroko — session started",
		"color":       0x3498db, // blue
		"description": "🔌 Connected",
		"fields": []any{
			map[string]any{"name": "Command", "value": fmt.Sprintf("`%s`", command), "inline": false},
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	return d.postEmbed(embed)
}

func (d *discordNotifier) NotifyEnd(logPath, command string, exitCode int, duration time.Duration) error {
	logContent, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}

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
			map[string]any{"name": "Command", "value": fmt.Sprintf("`%s`", command), "inline": true},
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

	pw, _ := mw.CreateFormField("payload_json")
	pw.Write(payloadJSON)

	fw, _ := mw.CreateFormFile("files[0]", filepath.Base(logPath))
	fw.Write(logContent)

	mw.Close()

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
		"text": fmt.Sprintf("🔌 *kuroko* session started: `%s`", command),
	})
}

func (s *slackNotifier) NotifyEnd(logPath, command string, exitCode int, duration time.Duration) error {
	logContent, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}
	icon := "✅"
	if exitCode != 0 {
		icon = "❌"
	}
	text := fmt.Sprintf("%s *kuroko* `%s` ended (exit %d, %s)\n```%s```",
		icon, command, exitCode, formatDuration(duration),
		truncate(string(logContent), 2500))
	return postJSON(s.webhookURL, map[string]string{"text": text})
}

// --- helpers ---

func doRequest(req *http.Request) error {
	resp, err := http.DefaultClient.Do(req)
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "...(truncated)...\n" + s[len(s)-max:]
}

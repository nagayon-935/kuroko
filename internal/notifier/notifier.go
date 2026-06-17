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

// Notifier sends session summaries to external services.
type Notifier interface {
	Notify(logPath string, command string, exitCode int) error
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

func (n *noopNotifier) Notify(string, string, int) error { return nil }

// --- Discord ---

type discordNotifier struct{ webhookURL string }

func (d *discordNotifier) Notify(logPath, command string, exitCode int) error {
	logContent, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}

	filename := filepath.Base(logPath)

	// Color and status label based on exit code
	color := 0x2ecc71 // green = success
	status := "Success"
	if exitCode != 0 {
		color = 0xe74c3c // red = failure
		status = fmt.Sprintf("Failed (exit %d)", exitCode)
	}

	embed := map[string]any{
		"title":       "kuroko — session ended",
		"color":       color,
		"description": status,
		"fields": []any{
			map[string]any{
				"name":   "Command",
				"value":  fmt.Sprintf("`%s`", command),
				"inline": false,
			},
			map[string]any{
				"name":   "Log file",
				"value":  filename,
				"inline": false,
			},
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	payloadJSON, err := json.Marshal(map[string]any{
		"embeds": []any{embed},
	})
	if err != nil {
		return err
	}

	// Multipart body: embed JSON + log file attachment
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	pw, _ := mw.CreateFormField("payload_json")
	pw.Write(payloadJSON)

	fw, _ := mw.CreateFormFile("files[0]", filename)
	fw.Write(logContent)

	mw.Close()

	req, err := http.NewRequest(http.MethodPost, d.webhookURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned %d", resp.StatusCode)
	}
	return nil
}

// --- Slack ---

type slackNotifier struct{ webhookURL string }

func (s *slackNotifier) Notify(logPath, command string, exitCode int) error {
	logContent, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}

	text := fmt.Sprintf("*kuroko session*: `%s` (exit %d)\n```%s```",
		command, exitCode, truncate(string(logContent), 2800))

	return postJSON(s.webhookURL, map[string]string{"text": text})
}

// --- helpers ---

func postJSON(url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "...(truncated)...\n" + s[len(s)-max:]
}

package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

	// Discord has a 2000-char message limit; truncate if needed
	content := fmt.Sprintf("```\nkuroko session: %s (exit %d)\n\n%s\n```",
		command, exitCode, truncate(string(logContent), 1800))

	payload := map[string]string{"content": content}
	return postJSON(d.webhookURL, payload)
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

	payload := map[string]string{"text": text}
	return postJSON(s.webhookURL, payload)
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

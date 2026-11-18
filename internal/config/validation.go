package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// ansiColor maps color name strings to ANSI foreground escape codes.
var ansiColor = map[string]string{
	"red":     "\x1b[31m",
	"yellow":  "\x1b[33m",
	"green":   "\x1b[32m",
	"cyan":    "\x1b[36m",
	"blue":    "\x1b[34m",
	"magenta": "\x1b[35m",
}

// BannerColor returns the ANSI foreground sequence, or an empty string for an unknown color.
func BannerColor(name string) string { return ansiColor[name] }

type Issue struct {
	Path     string
	Severity string
	Message  string
}

func (i Issue) String() string { return fmt.Sprintf("%s: %s: %s", i.Severity, i.Path, i.Message) }

func warning(path, message string) Issue {
	return Issue{Path: path, Severity: "warning", Message: message}
}

func (r *Report) validate(typeSource string) {
	warn := func(path, message string) { r.Issues = append(r.Issues, warning(path, message)) }
	switch r.Notifier.Type {
	case "none":
	case "discord", "slack":
		if r.Notifier.WebhookURL == "" {
			warn("notifier.webhook_url", "required for "+r.Notifier.Type)
		}
	default:
		warn(typeSource, "expected none, discord, or slack")
	}
	if r.Storage.CompressThresholdMB < 0 {
		warn("storage.compress_threshold_mb", "must be zero or greater")
	}
	if r.Storage.Rotation.MaxAgeDays < 0 {
		warn("storage.rotation.max_age_days", "must be zero or greater")
	}
	if r.Storage.Rotation.MaxTotalSizeMB < 0 {
		warn("storage.rotation.max_total_size_mb", "must be zero or greater")
	}
	for i, rule := range r.Banner.Rules {
		path := fmt.Sprintf("banner.rules[%d].", i)
		if rule.Match == "" {
			warn(path+"match", "must not be empty")
		}
		if BannerColor(rule.Color) == "" {
			warn(path+"color", "expected one of "+strings.Join(slices.Sorted(maps.Keys(ansiColor)), ", "))
		}
	}
}

func unknownKeys(data []byte) ([]Issue, error) {
	var root any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // Even an ignored field may contain a number too large for float64.
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	var issues []Issue
	walkKeys(root, reflect.TypeFor[Config](), "", &issues)
	return issues, nil
}

func walkKeys(value any, schema reflect.Type, path string, issues *[]Issue) {
	switch node := value.(type) {
	case []any:
		for i, item := range node {
			walkKeys(item, schema.Elem(), fmt.Sprintf("%s[%d]", path, i), issues)
		}
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(node)) {
			fieldPath := key
			if path != "" {
				fieldPath = path + "." + key
			}
			var fieldType reflect.Type
			for field := range schema.Fields() {
				name := field.Tag.Get("json")
				if strings.EqualFold(key, name) { // encoding/json also accepts case-insensitive matches.
					fieldType = field.Type
					break
				}
			}
			if fieldType != nil {
				walkKeys(node[key], fieldType, fieldPath, issues)
				continue
			}
			message := "unknown key"
			if closest := closestKey(key, schema); closest != "" {
				message += fmt.Sprintf("; did you mean %q?", closest)
			}
			*issues = append(*issues, warning(fieldPath, message))
		}
	}
}

func closestKey(key string, schema reflect.Type) string {
	length := utf8.RuneCountInString(key)
	closest, best := "", 3 // Suggest keys within two edits.
	for field := range schema.Fields() {
		name := field.Tag.Get("json")
		if length > len(name)+2 || len(name) > length+2 {
			continue
		}
		if d := editDistance(key, name); d < best {
			closest, best = name, d
		}
	}
	return closest
}

// editDistance computes Levenshtein distance to an ASCII JSON tag.
func editDistance(key, tag string) int {
	row := make([]int, len(tag)+1)
	for j := range row {
		row[j] = j
	}
	for _, ch := range key {
		diagonal := row[0]
		row[0]++
		for j := range len(tag) {
			cost := 1
			if ch == rune(tag[j]) {
				cost = 0
			}
			next := min(row[j]+1, row[j+1]+1, diagonal+cost)
			diagonal, row[j+1] = row[j+1], next
		}
	}
	return row[len(tag)]
}

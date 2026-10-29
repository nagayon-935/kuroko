package logger

import (
	"bytes"
	"regexp"
	"strings"
)

var (
	awsAccessKeyPattern = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	awsSecretKeyPattern = regexp.MustCompile(`(?i)(aws_secret_access_key|secret_key|secret)\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})["']?`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)Bearer\s+([a-zA-Z0-9_\-\.\+\/\\=]+)`)
	sqlPasswordPattern  = regexp.MustCompile(`(?i)identified\s+by\s+'([^']+)'`)
)

func redactSubmatch(line []byte, re *regexp.Regexp, group int, placeholder []byte) []byte {
	return re.ReplaceAllFunc(line, func(match []byte) []byte {
		if parts := re.FindSubmatch(match); len(parts) > group {
			return bytes.Replace(match, parts[group], placeholder, 1)
		}
		return match
	})
}

// redactor masks credentials in committed rows. It is stateful because a PEM
// private key spans many rows between its BEGIN and END markers.
type redactor struct {
	inPEMBlock bool
}

// redact returns line with any credentials it contains replaced by
// placeholders.
func (r *redactor) redact(line []byte) []byte {
	lineStr := string(line)

	// Stateful PEM Block redaction
	if strings.Contains(lineStr, "-----BEGIN") && strings.Contains(lineStr, "PRIVATE KEY-----") {
		r.inPEMBlock = true
		return []byte("[PEM PRIVATE KEY BLOCK STARTED - REDACTED]")
	}
	if r.inPEMBlock {
		if strings.Contains(lineStr, "-----END") && strings.Contains(lineStr, "PRIVATE KEY-----") {
			r.inPEMBlock = false
			return []byte("[PEM PRIVATE KEY BLOCK ENDED - REDACTED]")
		}
		return []byte("[PEM PRIVATE KEY DATA - REDACTED]")
	}

	line = awsAccessKeyPattern.ReplaceAll(line, []byte("[AWS_ACCESS_KEY_REDACTED]"))
	line = redactSubmatch(line, awsSecretKeyPattern, 2, []byte("[AWS_SECRET_KEY_REDACTED]"))
	line = redactSubmatch(line, bearerTokenPattern, 1, []byte("[TOKEN_REDACTED]"))
	line = redactSubmatch(line, sqlPasswordPattern, 1, []byte("[PASSWORD_REDACTED]"))
	return line
}

package logger

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
)

var (
	awsAccessKeyPattern = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	awsSecretKeyPattern = regexp.MustCompile(`(?i)(aws_secret_access_key|secret_key|secret)\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})["']?`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)Bearer\s+([a-zA-Z0-9_\-\.\+\/\\=]+)`)
	sqlPasswordPattern  = regexp.MustCompile(`(?i)identified\s+by\s+'([^']+)'`)

	// Network device configuration secrets (Cisco IOS/NX-OS, Arista EOS,
	// Junos). The value may be preceded by an encryption type ("7", "sha512")
	// and may be quoted, as Junos prints it.
	nwSecretValue = `(?:(?:\d{1,2}|sha512|sha256|md5|scrypt)\s+)?("[^"]*"|\S+)`
	// nwKeywordSecretPattern matches secrets introduced by a keyword that is
	// specific enough to be recognised anywhere on a line.
	nwKeywordSecretPattern = regexp.MustCompile(`(?i)\b(?:` +
		`enable\s+(?:secret|password)(?:\s+level\s+\d+)?` +
		`|username\s+\S+(?:\s+(?:privilege|role)\s+\S+)*\s+(?:secret|password)` +
		`|(?:tacacs|radius)-server\s+key` +
		`|key-string` +
		`|authentication-key` +
		`|pre-shared-key(?:\s+(?:ascii-text|hexadecimal))?` +
		`|encrypted-password` +
		`|neighbor\s+\S+\s+password` +
		`|message-digest-key\s+\d+\s+md5` +
		`|snmp-server\s+community` +
		`)\s+` + nwSecretValue)
	// nwBareSecretPattern matches the generic sub-mode forms (" password 7 …"
	// under a line, " key 7 …" under a server) only when they make up the
	// whole row, so prose mentioning a password is left alone. Values shorter
	// than four characters are key IDs ("key 1"), not secrets.
	nwBareSecretPattern = regexp.MustCompile(`(?i)^\s*(?:password|secret|key)\s+(?:(?:\d{1,2}|sha512|sha256|md5|scrypt)\s+)?("[^"]{4,}"|\S{4,})\s*;?\s*$`)
)

// maxPEMBlockLines bounds how many rows one PEM private key may redact. The
// largest keys in practice (RSA 16384) stay under 200 lines.
const maxPEMBlockLines = 512

func redactSubmatch(line []byte, re *regexp.Regexp, group int, placeholder []byte) []byte {
	return re.ReplaceAllFunc(line, func(match []byte) []byte {
		idx := re.FindSubmatchIndex(match)
		if len(idx) > 2*group+1 && idx[2*group] >= 0 {
			start, end := idx[2*group], idx[2*group+1]
			out := make([]byte, 0, len(match)-(end-start)+len(placeholder))
			out = append(out, match[:start]...)
			out = append(out, placeholder...)
			out = append(out, match[end:]...)
			return out
		}
		return match
	})
}

// redactor masks credentials in committed rows. It is stateful because a PEM
// private key spans many rows between its BEGIN and END markers.
type redactor struct {
	inPEMBlock bool
	pemLines   int // rows redacted in the current PEM block
}

// beginPEM enters a PEM block.
func (r *redactor) beginPEM() {
	r.inPEMBlock = true
	r.pemLines = 0
}

// looksLikePEMBody reports whether line can belong to a PEM block: a
// base64 row, an encryption header of the legacy format, or a blank row.
// Anything with a space in it, or anything that reads as a prompt, cannot —
// which is how a block whose END line never arrived (head -2 key.pem, an
// interrupted cat) is recognised as over instead of redacting every row for
// the rest of the session.
func looksLikePEMBody(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return true
	}
	if bytes.HasPrefix(trimmed, []byte("Proc-Type: ")) || bytes.HasPrefix(trimmed, []byte("DEK-Info: ")) {
		return true
	}
	if bytes.ContainsAny(trimmed, " \t") {
		return false
	}
	_, _, info := SplitPromptInfo(trimmed)
	return info.Kind == KindNone
}

// redact returns line with any credentials it contains replaced by
// placeholders.
func (r *redactor) redact(line []byte) []byte {
	lineStr := string(line)

	// Stateful PEM Block redaction
	if beginIdx := strings.Index(lineStr, "-----BEGIN"); beginIdx >= 0 && strings.Contains(lineStr[beginIdx:], "PRIVATE KEY-----") {
		rest := lineStr[beginIdx:]
		if endIdx := strings.Index(rest, "-----END"); endIdx >= 0 && strings.Contains(rest[endIdx:], "PRIVATE KEY-----") {
			r.inPEMBlock = false
			return []byte("[PEM PRIVATE KEY BLOCK ENDED - REDACTED]")
		}
		r.beginPEM()
		return []byte("[PEM PRIVATE KEY BLOCK STARTED - REDACTED]")
	}
	if r.inPEMBlock {
		if strings.Contains(lineStr, "-----END") && strings.Contains(lineStr, "PRIVATE KEY-----") {
			r.inPEMBlock = false
			return []byte("[PEM PRIVATE KEY BLOCK ENDED - REDACTED]")
		}
		r.pemLines++
		if r.pemLines <= maxPEMBlockLines && looksLikePEMBody(line) {
			return []byte("[PEM PRIVATE KEY DATA - REDACTED]")
		}
		// The block ended without its END line; this row is ordinary output.
		r.inPEMBlock = false
	}

	line = awsAccessKeyPattern.ReplaceAll(line, []byte("[AWS_ACCESS_KEY_REDACTED]"))
	line = redactSubmatch(line, awsSecretKeyPattern, 2, []byte("[AWS_SECRET_KEY_REDACTED]"))
	line = redactSubmatch(line, bearerTokenPattern, 1, []byte("[TOKEN_REDACTED]"))
	line = redactSubmatch(line, sqlPasswordPattern, 1, []byte("[PASSWORD_REDACTED]"))
	line = redactSubmatch(line, nwKeywordSecretPattern, 1, []byte("[SECRET_REDACTED]"))
	line = redactSubmatch(line, nwBareSecretPattern, 1, []byte("[SECRET_REDACTED]"))
	return line
}

// RedactText masks credentials in a whole log text, line by line, the same
// way redaction does while a session is recorded. It is used for log content
// that leaves the machine, such as webhook notifications.
func RedactText(text []byte) []byte {
	var r redactor
	lines := bytes.SplitAfter(text, []byte("\n"))
	out := make([]byte, 0, len(text))
	for _, line := range lines {
		body, hasNewline := bytes.CutSuffix(line, []byte("\n"))
		out = append(out, r.redact(body)...)
		if hasNewline {
			out = append(out, '\n')
		}
	}
	return out
}

// RedactTo copies src to dst line by line with credentials masked, like
// RedactText, without holding the whole text in memory.
func RedactTo(dst io.Writer, src io.Reader) error {
	var r redactor
	br := bufio.NewReader(src)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			body, hasNewline := bytes.CutSuffix(line, []byte("\n"))
			out := r.redact(body)
			if hasNewline {
				out = append(out, '\n')
			}
			if _, werr := dst.Write(out); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

package logger

import (
	"regexp"
	"strings"
)

// This file is a verbatim copy of the redactor as it was before the keyword
// prefilter was added. It is the test oracle: the optimized redactor must
// produce byte-for-byte identical output.

func legacyRedactSubmatch(line []byte, re *regexp.Regexp, group int, placeholder []byte) []byte {
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

type legacyRedactor struct {
	inPEMBlock bool
	pemLines   int // rows redacted in the current PEM block
}

func (r *legacyRedactor) beginPEM() {
	r.inPEMBlock = true
	r.pemLines = 0
}

func (r *legacyRedactor) redact(line []byte) []byte {
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
	line = legacyRedactSubmatch(line, awsSecretKeyPattern, 2, []byte("[AWS_SECRET_KEY_REDACTED]"))
	line = legacyRedactSubmatch(line, bearerTokenPattern, 1, []byte("[TOKEN_REDACTED]"))
	line = legacyRedactSubmatch(line, sqlPasswordPattern, 1, []byte("[PASSWORD_REDACTED]"))
	line = legacyRedactSubmatch(line, nwKeywordSecretPattern, 1, []byte("[SECRET_REDACTED]"))
	line = legacyRedactSubmatch(line, nwBareSecretPattern, 1, []byte("[SECRET_REDACTED]"))
	return line
}

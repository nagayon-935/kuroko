package logger

// Redaction keyword prefilter. Every regex rule can only match a line that
// contains one of a few literal keywords, so one cheap scan lets ordinary
// output skip the regex engine (and its backtracking) entirely.

// Keyword bits reported by scanRedactKeywords.
const (
	kwAKIA uint8 = 1 << iota
	kwSecret
	kwBearer
	kwIdentified
	kwPassword
	kwKey
	kwCommunity

	kwAll = kwAKIA | kwSecret | kwBearer | kwIdentified | kwPassword | kwKey | kwCommunity
)

// scanRedactKeywords returns the set of keywords present in line. "AKIA" is
// matched case-sensitively, the others ASCII case-insensitively.
//
// A byte >= 0x80 opens every gate: Go's (?i) uses Unicode simple folding
// (U+017F matches "s", U+212A matches "k"), so a non-ASCII line can match
// rules without containing their ASCII keyword.
func scanRedactKeywords(line []byte) uint8 {
	var mask uint8
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c >= 0x80 {
			return kwAll
		}
		switch c {
		case 'A':
			if hasPrefixFold(line[i:], "AKIA", true) {
				mask |= kwAKIA
			}
		case 'S', 's':
			if hasPrefixFold(line[i:], "secret", false) {
				mask |= kwSecret
			}
		case 'B', 'b':
			if hasPrefixFold(line[i:], "bearer", false) {
				mask |= kwBearer
			}
		case 'I', 'i':
			if hasPrefixFold(line[i:], "identified", false) {
				mask |= kwIdentified
			}
		case 'P', 'p':
			if hasPrefixFold(line[i:], "password", false) {
				mask |= kwPassword
			}
		case 'K', 'k':
			if hasPrefixFold(line[i:], "key", false) {
				mask |= kwKey
			}
		case 'C', 'c':
			if hasPrefixFold(line[i:], "community", false) {
				mask |= kwCommunity
			}
		}
	}
	return mask
}

// hasPrefixFold reports whether b starts with kw. kw must be ASCII; unless
// exact is set, letters compare case-insensitively (kw is lower case).
func hasPrefixFold(b []byte, kw string, exact bool) bool {
	if len(b) < len(kw) {
		return false
	}
	for i := 0; i < len(kw); i++ {
		c := b[i]
		if !exact && 'A' <= c && c <= 'Z' {
			c |= 0x20
		}
		if c != kw[i] {
			return false
		}
	}
	return true
}

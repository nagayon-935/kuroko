package logger

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

// LogSubdir groups by the command and typed target, without SSH resolution.
func LogSubdir(args []string) string {
	if len(args) == 0 {
		return "unknown"
	}
	return LogSubdirFor(args[0], TargetAddress(args))
}

func LogSubdirFor(command, target string) string {
	command = filepath.Base(command)
	kind := safeComponent(command)
	switch command {
	case "ssh":
		if i := strings.LastIndex(target, "@"); i >= 0 {
			target = target[i+1:]
		}
		return filepath.Join(kind, safeComponent(target))
	case "screen":
		return filepath.Join(kind, safeComponent(filepath.Base(target)))
	default:
		return kind
	}
}

// Percent encoding is reversible and avoids collisions between e.g. a/b
// and a_b. Components cannot become separators, . or .., or hidden folders.
func safeComponent(value string) string {
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || (c == '.' && i > 0) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	encoded := b.String()
	if len(encoded) > 180 {
		encoded = fmt.Sprintf("%s-%x", encoded[:150], sha256.Sum256([]byte(value)))
	}
	return encoded
}

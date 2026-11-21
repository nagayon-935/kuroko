package logger

import (
	"bytes"
	"testing"
)

func BenchmarkSplitPromptLongLine(b *testing.B) {
	line := bytes.Repeat([]byte("a"), 100_000)
	for i := 0; i < b.N; i++ {
		SplitPrompt(line)
	}
}

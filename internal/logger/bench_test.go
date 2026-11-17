package logger

import (
	"bytes"
	"fmt"
	"testing"
)

const (
	benchTotalBytes = 8 << 20
	benchChunkSize  = 4 << 10
	benchScreenRows = 50
)

// benchStream builds about benchTotalBytes of realistic interface-status
// output, the kind a router prints for "show interfaces".
func benchStream() []byte {
	var buf bytes.Buffer
	for i := 0; buf.Len() < benchTotalBytes; i++ {
		fmt.Fprintf(&buf, "GigabitEthernet0/%d is up, line protocol is up (connected)\r\n", i%48)
	}
	return buf.Bytes()
}

func benchmarkLoggerThroughput(b *testing.B, args []string, redact bool) {
	stream := benchStream()
	dir := b.TempDir()
	b.SetBytes(int64(len(stream)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := New(dir, args, redact)
		if err != nil {
			b.Fatal(err)
		}
		l.SetScreenRows(benchScreenRows)
		for off := 0; off < len(stream); off += benchChunkSize {
			end := min(off+benchChunkSize, len(stream))
			if _, err := l.Write(stream[off:end]); err != nil {
				b.Fatal(err)
			}
		}
		if err := l.Close(0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoggerShell(b *testing.B) {
	benchmarkLoggerThroughput(b, []string{"bash"}, false)
}

func BenchmarkLoggerNetwork(b *testing.B) {
	benchmarkLoggerThroughput(b, []string{"ssh", "sw1"}, false)
}

func BenchmarkLoggerNetworkRedacted(b *testing.B) {
	benchmarkLoggerThroughput(b, []string{"ssh", "sw1"}, true)
}

package logger

import (
	"testing"
)

var benchSplitSink PromptInfo

func BenchmarkSplitPromptInfoInterfaceLine(b *testing.B) {
	line := []byte("GigabitEthernet0/1 is up, line protocol is up (connected)")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, benchSplitSink = SplitPromptInfo(line)
	}
}

func BenchmarkSplitPromptInfoPromptLine(b *testing.B) {
	line := []byte("edge-sw01.dc1(config-if)#show running-config")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, benchSplitSink = SplitPromptInfo(line)
	}
}

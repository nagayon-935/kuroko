package logstore

import "testing"

func TestByteSizeFormatsUnitBoundaries(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024*1024 - 1, "1024.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{1 << 40, "1.0 TiB"},
		{1 << 50, "1.0 PiB"},
		{1 << 60, "1.0 EiB"},
		{1<<63 - 1, "8.0 EiB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := byteSize(tt.size); got != tt.want {
				t.Errorf("byteSize(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

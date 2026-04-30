package config

import "testing"

func TestParseMemoryLimit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
		ok    bool
	}{
		{name: "empty", input: "", want: 0, ok: true},
		{name: "mebibytes", input: "64MiB", want: 64 * 1024 * 1024, ok: true},
		{name: "megabytes", input: "128MB", want: 128 * 1000 * 1000, ok: true},
		{name: "bytes", input: "4096", want: 4096, ok: true},
		{name: "off", input: "off", want: 1<<63 - 1, ok: true},
		{name: "invalid", input: "wat", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMemoryLimit(tt.input)
			if tt.ok && err != nil {
				t.Fatalf("ParseMemoryLimit() error = %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("ParseMemoryLimit() error = nil, want error")
			}
			if tt.ok && got != tt.want {
				t.Fatalf("ParseMemoryLimit() = %d, want %d", got, tt.want)
			}
		})
	}
}

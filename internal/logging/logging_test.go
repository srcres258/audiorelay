package logging

import (
	"bytes"
	"strings"
	"testing"

	"log/slog"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  slog.Level
	}{
		{input: "debug", want: slog.LevelDebug},
		{input: "info", want: slog.LevelInfo},
		{input: "warn", want: slog.LevelWarn},
		{input: "warning", want: slog.LevelWarn},
		{input: "error", want: slog.LevelError},
	}

	for _, tt := range tests {
		got, err := ParseLevel(tt.input)
		if err != nil {
			t.Errorf("ParseLevel(%q) error = %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}

	if _, err := ParseLevel("trace"); err == nil {
		t.Fatal("ParseLevel(trace) error = nil, want error")
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := New("info", &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Info("hello", "component", "test")

	if !strings.Contains(output.String(), "level=INFO") || !strings.Contains(output.String(), "msg=hello") {
		t.Errorf("logger output = %q, want structured info record", output.String())
	}

	if _, err := New("invalid", &output); err == nil {
		t.Fatal("New(invalid level) error = nil, want error")
	}
	if _, err := New("info", nil); err == nil {
		t.Fatal("New(nil output) error = nil, want error")
	}
}

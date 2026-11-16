// Package logging provides the process-wide structured logger used by the
// relay control plane and media pipeline.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// DefaultLevel is the default verbosity for the command-line program.
const DefaultLevel = "info"

// New creates a text logger suitable for command-line and systemd output.
// Accepted levels are debug, info, warn, and error.
func New(level string, output io.Writer) (*slog.Logger, error) {
	parsed, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	if output == nil {
		return nil, fmt.Errorf("logging output must not be nil")
	}

	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{
		Level: parsed,
	})), nil
}

// ParseLevel converts the user-facing log-level value to a slog level.
func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: must be debug, info, warn, or error", value)
	}
}

package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"snmptrap-relay/internal/model"
)

func New(cfg model.LoggingConfig) (*slog.Logger, error) {
	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var writer io.Writer = os.Stdout
	if file := cfg.File; file != "" {
		fh, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		writer = fh
	}

	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	case "", "text":
		handler = slog.NewTextHandler(writer, opts)
	default:
		return nil, fmt.Errorf("unsupported logging format %q", cfg.Format)
	}
	return slog.New(handler), nil
}

func parseLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"snmptrap-relay/internal/model"
)

type Resource struct {
	Logger *slog.Logger
	Closer io.Closer
}

func New(cfg model.LoggingConfig) (*slog.Logger, error) {
	resource, err := NewResource(cfg)
	if err != nil {
		return nil, err
	}
	return resource.Logger, nil
}

func NewResource(cfg model.LoggingConfig) (Resource, error) {
	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var writer io.Writer = os.Stdout
	var closer io.Closer
	if file := cfg.File; file != "" {
		fh, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return Resource{}, err
		}
		writer = fh
		closer = fh
	}

	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	case "", "text":
		handler = slog.NewTextHandler(writer, opts)
	default:
		if closer != nil {
			_ = closer.Close()
		}
		return Resource{}, fmt.Errorf("unsupported logging format %q", cfg.Format)
	}
	return Resource{Logger: slog.New(handler), Closer: closer}, nil
}

// NewAlertsWriter opens the alerts-only log file configured in LoggingConfig
// and returns a writer that can be used to append alert lines. If no alerts
// file is configured, (nil, nil) is returned.
func NewAlertsWriter(cfg model.LoggingConfig) (io.WriteCloser, error) {
	if cfg.AlertsFile == "" {
		return nil, nil
	}
	fh, err := os.OpenFile(cfg.AlertsFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return fh, nil
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

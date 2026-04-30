package logging

import (
	"os"
	"path/filepath"
	"testing"

	"snmptrap-relay/internal/model"
)

func TestNewCreatesPrivateLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay.log")

	logger, err := New(model.LoggingConfig{File: path, Format: "text"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if logger == nil {
		t.Fatal("New() logger = nil")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log file mode = %o, want 600", got)
	}
}

func TestNewResourceReturnsCloserForFileOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay.log")

	resource, err := NewResource(model.LoggingConfig{File: path, Format: "text"})
	if err != nil {
		t.Fatalf("NewResource() error = %v", err)
	}
	if resource.Logger == nil {
		t.Fatal("NewResource() logger = nil")
	}
	if resource.Closer == nil {
		t.Fatal("NewResource() closer = nil, want non-nil")
	}
	if err := resource.Closer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNewResourceReturnsNilCloserForStdout(t *testing.T) {
	resource, err := NewResource(model.LoggingConfig{Format: "text"})
	if err != nil {
		t.Fatalf("NewResource() error = %v", err)
	}
	if resource.Logger == nil {
		t.Fatal("NewResource() logger = nil")
	}
	if resource.Closer != nil {
		t.Fatal("NewResource() closer != nil, want nil")
	}
}

func TestNewAlertsWriterCreatesPrivateLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alerts.log")

	writer, err := NewAlertsWriter(model.LoggingConfig{AlertsFile: path})
	if err != nil {
		t.Fatalf("NewAlertsWriter() error = %v", err)
	}
	defer writer.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("alerts log file mode = %o, want 600", got)
	}
}
